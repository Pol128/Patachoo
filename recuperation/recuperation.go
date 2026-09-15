// Package recuperation va chercher, pour le compte de l'utilisateur, la page
// d'une URL qu'il a collée. Il rend des octets, il ne les interprète pas :
// l'extraction de la recette est l'affaire de jsonld.
//
// L'URL vient de l'utilisateur et c'est le serveur qui va la chercher : c'est
// un SSRF par construction, et c'est le point le plus dangereux du produit
// (DOD.md §3). Le refus des adresses non routables se fait donc sur l'adresse
// résolue, juste avant le connect, et non sur le nom d'hôte — voir controle.
//
// L'échec est l'une des six causes nommées, jamais un message libre : PATA-9
// les traduit pour l'utilisateur sans avoir à les analyser.
package recuperation

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Les six causes d'échec. Ce sont des chaînes stables : elles font partie du
// contrat que lit l'appelant.
const (
	// Injoignable : DNS muet, connexion refusée, ou trop de redirections.
	// Aucune page n'a été atteinte, et rien ne dit qu'une seconde tentative
	// ferait mieux.
	Injoignable = "injoignable"
	// RefusHTTP : le site a répondu, mais par un refus. Le code obtenu est
	// porté par l'erreur — 403 et 404 n'appellent pas la même réaction.
	RefusHTTP = "refus_http"
	// RefuseeParPolitique : l'URL vise une adresse que nous n'allons pas
	// chercher (boucle locale, plage privée, métadonnées cloud) ou un schéma
	// que nous ne suivons pas. Vaut aussi après une redirection.
	RefuseeParPolitique = "refusee_par_politique"
	// RobotsInterdit : le site refuse explicitement, par son robots.txt.
	RobotsInterdit = "robots_interdit"
	// DelaiDepasse : le site répond, mais trop lentement pour qu'on l'attende.
	DelaiDepasse = "delai_depasse"
	// TailleMax : la page dépasse le plafond ; la lecture s'est arrêtée là.
	TailleMax = "taille_max"
)

// Agent nous nomme auprès des sites visités, et donne où écrire si l'un d'eux
// veut nous en empêcher. C'est aussi le jeton que robots.txt peut viser.
const Agent = "Patachoo/0.1 (+https://github.com/Pol128/Patachoo)"

const (
	// DelaiMaxDefaut borne le temps de réseau d'un appel — robots.txt et page
	// confondus. L'attente qu'une cadence impose n'y entre pas : voir echange.
	DelaiMaxDefaut = 10 * time.Second
	// TailleMaxDefaut borne le corps lu. Une page qui ne s'arrête jamais ne
	// doit pas emporter le serveur.
	TailleMaxDefaut = 5 << 20
	// redirectionsMax borne la chaîne de sauts. Au-delà, c'est une boucle.
	redirectionsMax = 5
)

// Page est ce qu'on rapporte quand tout s'est bien passé. URLFinale est celle du
// dernier saut, pas celle qui a été soumise : c'est elle que PATA-9 garde en
// source_url.
type Page struct {
	Corps       []byte
	URLFinale   string
	TypeContenu string
}

// Erreur porte la cause nommée, le code HTTP quand il y en a eu un, et l'URL sur
// laquelle l'échec s'est produit — utile quand il survient après une
// redirection. L'appelant la lit par errors.As, jamais en cherchant une
// sous-chaîne dans le message.
type Erreur struct {
	Cause string
	Code  int
	URL   string
}

func (e *Erreur) Error() string {
	if e.Code != 0 {
		return e.Cause + " (" + http.StatusText(e.Code) + ") : " + e.URL
	}
	return e.Cause + " : " + e.URL
}

// options porte ce qui se règle. Le délai et la taille maximale en sont parce
// que des constantes en dur ne se testent pas : personne n'écrit un test qui
// attend dix secondes ou qui sert cinq mébioctets.
type options struct {
	delaiMax  time.Duration
	tailleMax int64
	// resout traduit un nom en adresses. Injectable pour que les tests ne
	// dépendent pas du DNS de la machine.
	resout func(context.Context, string) ([]netip.Addr, error)
	// exception lève la politique d'IP pour des adresses précises. Réservée aux
	// tests, qui n'ont que la boucle locale pour monter un serveur.
	exception func(netip.AddrPort) bool
	// transport court-circuite la pile réseau. Réservé aux tests qui vérifient
	// qu'aucune requête ne part.
	transport http.RoundTripper
	// cadence espace les requêtes sortantes, hôte par hôte. Nil : elles
	// partent dès qu'on les fait.
	cadence Cadence
	// robots garde le robots.txt déjà lu de chaque hôte. Nil : il est
	// redemandé à chaque appel.
	robots *RobotsRetenus
}

// Option règle un appel.
type Option func(*options)

// AvecDelaiMax borne le temps de réseau d'un appel, robots.txt compris. Ce
// budget est partagé entre les échanges, et l'attente qu'une cadence impose n'y
// entre pas — voir echange.
func AvecDelaiMax(d time.Duration) Option {
	return func(o *options) { o.delaiMax = d }
}

// AvecTailleMax borne le corps lu.
func AvecTailleMax(octets int64) Option {
	return func(o *options) { o.tailleMax = octets }
}

// Cadence est le rythme que l'appelant impose aux requêtes que ce paquet émet,
// hôte par hôte.
//
// Un appel en émet deux : le robots.txt de l'hôte, que l'appelant n'a pas
// demandé, puis la page. Sans cadence elles partent collées — un appelant qui
// espace ses appels d'une seconde en envoie tout de même deux dans le même
// instant, et sa politesse ne vaut que pour la couture qu'il tient. Avec,
// chacun des deux échanges attend son tour, robots.txt compris.
//
// Retiens rapporte le Crawl-delay lu dans le robots.txt. Il est rapporté avant
// la requête de page, et non après l'appel : c'est ce qui le fait valoir dès
// cette page-là.
type Cadence interface {
	AttendSonTour(ctx context.Context, hote string) error
	Retiens(hote string, annonce time.Duration)
}

// AvecCadence fait passer chaque requête sortante par le tour de rôle que
// l'appelant tient.
//
// L'attente qu'elle impose n'est comptée dans aucune borne de temps : voir
// echange.
func AvecCadence(c Cadence) Option {
	return func(o *options) { o.cadence = c }
}

// AvecRobotsRetenus garde le robots.txt de chaque hôte au lieu de le redemander
// à chaque page.
//
// Une fournée de vingt pages sur un même site lui coûte alors vingt-et-une
// requêtes et non quarante. Le cache appartient à l'appelant : c'est lui qui
// décide de sa durée de vie — celle d'une fournée —, et rien ici ne le périme.
func AvecRobotsRetenus(r *RobotsRetenus) Option {
	return func(o *options) { o.robots = r }
}

// Les trois options qui suivent ouvrent les points d'injection du paquet, et
// ne servent qu'aux tests : ceux d'ici, et ceux des appelants qui doivent
// prouver qu'une URL qu'ils reçoivent passe bien par ce récupérateur-ci.
//
// Exportées faute de mieux. Un appelant d'un autre paquet n'a aucun autre
// moyen d'atteindre un serveur httptest : il écoute sur la boucle locale, que
// la politique refuse par construction. Un faux récupérateur à sa place ne
// prouverait rien — il passerait encore le jour où l'image cesserait de
// passer par ici.
//
// Aucun chemin de production ne les emploie, et c'est ce qui les rend sûres :
// les valeurs par défaut ne dépendent d'aucune d'elles.

// AvecResolution remplace la traduction d'un nom d'hôte en adresses. Un test
// qui la fournit ne dépend plus du DNS de la machine, et peut faire pointer un
// nom d'apparence publique où il veut.
func AvecResolution(resout func(context.Context, string) ([]netip.Addr, error)) Option {
	return func(o *options) { o.resout = resout }
}

// AvecExceptionDePolitique lève l'interdiction d'adresse pour les seuls
// couples adresse:port dont permet dit vrai. Tout le reste — y compris une
// autre adresse de boucle locale — reste refusé : c'est ce qui permet de
// tester une redirection vers 127.0.0.1 depuis un serveur qui y vit.
func AvecExceptionDePolitique(permet func(netip.AddrPort) bool) Option {
	return func(o *options) { o.exception = permet }
}

// AvecTransport remplace la pile HTTP. Sert au piège : un test qui n'attend
// aucune requête sortante en pose un qui le fait échouer s'il est appelé.
func AvecTransport(rt http.RoundTripper) Option {
	return func(o *options) { o.transport = rt }
}

// errAdresseRefusee remonte du composeur jusqu'ici à travers la pile HTTP. Elle
// ne sort jamais du paquet : elle devient RefuseeParPolitique.
var errAdresseRefusee = errors.New("adresse refusée par la politique de sécurité")

// errRedirectionArretee interrompt le client HTTP ; le motif détaillé est posé
// dans recuperateur.refus juste avant.
var errRedirectionArretee = errors.New("redirection arrêtée")

// Recupere va chercher la page d'adresse, en respectant le robots.txt de son
// hôte, et rend son corps ou l'une des six causes nommées.
func Recupere(ctx context.Context, adresse string, choix ...Option) (Page, error) {
	o := options{
		delaiMax:  DelaiMaxDefaut,
		tailleMax: TailleMaxDefaut,
		resout:    resolutionSysteme,
	}
	for _, regle := range choix {
		regle(&o)
	}

	cible, err := analyseURL(adresse)
	if err != nil {
		// Rien ne part : la validation précède le réseau.
		return Page{}, &Erreur{Cause: RefuseeParPolitique, URL: adresse}
	}

	r := &recuperateur{o: o, restant: o.delaiMax}
	r.client = &http.Client{Transport: r.pile(), CheckRedirect: r.verifieRedirection}
	// Le transport est propre à l'appel et rien ne le partage : passé ce
	// retour, plus personne ne peut réutiliser ses connexions inactives, mais
	// elles vivraient encore quatre-vingt-dix secondes. Les deux requêtes de
	// l'appel — robots.txt puis la page — sont faites à ce moment-là, et
	// page() a déjà lu le corps en entier dans Page.Corps : fermer ici ne
	// coupe aucune lecture en cours et ne coûte aucune réutilisation réelle.
	defer r.client.CloseIdleConnections()

	if refus := r.robotsInterdit(ctx, cible); refus != nil {
		return Page{}, refus
	}
	return r.page(ctx, cible)
}

// echange ouvre un aller-retour : il attend le tour de l'hôte, puis borne cet
// échange-là par ce qu'il reste du budget de temps réseau de l'appel.
//
// Deux exigences se croisent ici, et c'est le budget qui les tient ensemble.
//
// L'attente est prise avant la borne, et non dedans. Prise dedans, elle se
// consommerait sur le compte du délai maximal : un Crawl-delay plus long que lui
// ferait échouer toutes les pages de l'hôte en delai_depasse au lieu de les
// espacer, et la plage que delaiAnnonceMax accepte — jusqu'à cinq minutes —
// serait injouable.
//
// Mais la borne reste celle de l'appel entier, et non celle d'un échange :
// chaque échange rend au budget ce qu'il n'a pas consommé, et le suivant part
// avec ce qui reste. Une borne par échange doublerait le pire cas — vingt
// secondes au lieu de dix par défaut, robots.txt puis page —, et c'est l'import
// unitaire de PATA-9 qui le paierait, devant un utilisateur qui attend sa page.
//
// Un budget épuisé donne un contexte déjà échu : la requête ne part pas, et
// l'échec se nomme comme un dépassement de délai, ce qu'il est.
func (r *recuperateur) echange(ctx context.Context, hote string) (context.Context, context.CancelFunc, error) {
	if r.o.cadence != nil {
		if err := r.o.cadence.AttendSonTour(ctx, hote); err != nil {
			return nil, nil, err
		}
	}

	debut := time.Now()
	borne, arrete := context.WithTimeout(ctx, r.restant)
	return borne, func() {
		r.restant -= time.Since(debut)
		arrete()
	}, nil
}

// analyseURL n'accepte qu'une URL absolue de schéma http ou https, avec un hôte.
func analyseURL(adresse string) (*url.URL, error) {
	u, err := url.Parse(adresse)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("schéma non suivi : " + u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("URL sans hôte")
	}
	return u, nil
}

// recuperateur tient l'état d'un appel : un client, et le motif du refus qu'une
// redirection a pu déclencher. Il ne sert qu'à un appel, jamais partagé.
type recuperateur struct {
	o      options
	client *http.Client
	refus  *Erreur
	// restant est le temps de réseau qu'il reste à cet appel. Un seul
	// récupérateur par appel, et un seul appel à la fois dessus : rien ne le
	// partage entre goroutines.
	restant time.Duration
}

// pile monte le transport HTTP. Le proxy est retiré : un proxy déclaré dans
// l'environnement ferait porter la politique d'IP sur lui plutôt que sur la
// vraie destination.
func (r *recuperateur) pile() http.RoundTripper {
	if r.o.transport != nil {
		return r.o.transport
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = r.compose
	return t
}

// compose résout le nom puis compose vers l'adresse obtenue, littéralement. La
// politique, elle, est appliquée par controle : composer vers un littéral déjà
// résolu ne laisse aucune fenêtre entre la résolution et le connect.
func (r *recuperateur) compose(ctx context.Context, reseau, adresse string) (net.Conn, error) {
	hote, port, err := net.SplitHostPort(adresse)
	if err != nil {
		return nil, err
	}

	var adresses []netip.Addr
	if ip, err := netip.ParseAddr(hote); err == nil {
		adresses = []netip.Addr{ip}
	} else if adresses, err = r.o.resout(ctx, hote); err != nil {
		return nil, err
	}
	if len(adresses) == 0 {
		return nil, errors.New("aucune adresse pour " + hote)
	}

	composeur := &net.Dialer{Timeout: r.o.delaiMax, Control: r.controle}
	var dernier error
	for _, a := range adresses {
		conn, err := composeur.DialContext(ctx, reseau, net.JoinHostPort(a.String(), port))
		if err == nil {
			return conn, nil
		}
		if errors.Is(err, errAdresseRefusee) {
			// Une adresse interdite refuse l'hôte : essayer la suivante
			// reviendrait à laisser le site choisir laquelle nous atteignons.
			return nil, err
		}
		dernier = err
	}
	return nil, dernier
}

// controle est le gardien : il reçoit l'ip:port juste avant le connect. C'est le
// seul endroit où la politique s'applique, et il vaut pour la requête initiale,
// pour robots.txt et pour chaque redirection sans être réécrit trois fois.
func (r *recuperateur) controle(_, adresse string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(adresse)
	if err != nil {
		return errAdresseRefusee
	}
	if r.o.exception != nil && r.o.exception(ap) {
		return nil
	}
	if adresseInterdite(ap.Addr()) {
		return errAdresseRefusee
	}
	return nil
}

// plagesReservees complète les prédicats de netip, qui ne connaissent ni la
// plage partagée du RFC 6598 ni les plages que l'IANA garde pour elle. Compilées
// une fois : MustParsePrefix à chaque appel coûterait un analyseur par connect.
var plagesReservees = []netip.Prefix{
	// 100.64.0.0/10, la plage partagée du RFC 6598. Tailscale y numérote les
	// pairs d'un tailnet, et beaucoup de fournisseurs l'emploient en CGNAT :
	// c'est le réseau privé de l'utilisateur, qu'IsPrivate ne couvre pas.
	netip.MustParsePrefix("100.64.0.0/10"),
	// 0.0.0.0/8, le « this network » du RFC 1122. IsUnspecified n'en connaît
	// que la première adresse.
	netip.MustParsePrefix("0.0.0.0/8"),
	// 198.18.0.0/15, le banc d'essai du RFC 2544.
	netip.MustParsePrefix("198.18.0.0/15"),
	// 240.0.0.0/4, réservée par le RFC 1112 — l'adresse de diffusion
	// 255.255.255.255 comprise.
	netip.MustParsePrefix("240.0.0.0/4"),
}

// Les trois préfixes qui enferment une IPv4 dans une IPv6 et que netip.Addr.Unmap
// ne connaît pas — il ne réduit que la forme mappée ::ffff:0:0/96.
var (
	// prefixeNAT64 (RFC 6052) est ce par quoi un hôte IPv6 seul joint
	// l'Internet v4, à travers une passerelle NAT64/DNS64.
	prefixeNAT64 = netip.MustParsePrefix("64:ff9b::/96")
	// prefixe6to4 porte l'IPv4 du relais dans les bits 16 à 47.
	prefixe6to4 = netip.MustParsePrefix("2002::/16")
	// prefixeCompatibleV4 est la forme dépréciée par le RFC 4291 : le noyau
	// refuse de l'atteindre. Elle est réduite par cohérence.
	prefixeCompatibleV4 = netip.MustParsePrefix("::/96")
)

// reduite ramène une adresse à l'IPv4 qu'elle enferme, comme Unmap le fait pour
// la forme mappée. Elle rend l'adresse telle quelle si celle-ci n'enferme rien.
func reduite(a netip.Addr) netip.Addr {
	a = a.Unmap()
	o := a.As16()
	switch {
	case prefixeNAT64.Contains(a), prefixeCompatibleV4.Contains(a):
		return netip.AddrFrom4([4]byte(o[12:16]))
	case prefixe6to4.Contains(a):
		return netip.AddrFrom4([4]byte(o[2:6]))
	}
	return a
}

// adresseInterdite dit les adresses que nous n'allons pas chercher : la boucle
// locale, les plages privées, le lien-local — dont 169.254.169.254, qui sert les
// métadonnées des hébergeurs — le multicast, l'adresse non spécifiée, et les
// plages de plagesReservees : la plage partagée du RFC 6598, où vit un tailnet,
// « this network », le banc d'essai et la plage réservée de l'IANA.
//
// Quatre écritures enferment une IPv4 dans une IPv6, et chacune est ramenée à ce
// qu'elle porte avant l'examen : sans quoi elle passerait pour une adresse v6
// quelconque, qu'une passerelle traduirait ensuite vers ce que nous refusons. La
// forme mappée ::ffff:127.0.0.1, le préfixe NAT64 64:ff9b::/96, 6to4 2002::/16
// et la forme compatible v4 ::/96. Les plages de plagesReservees sont examinées
// après cette réduction, comme les prédicats de netip.
//
// L'examen porte sur les deux formes, parce que réduire en ouvrirait une autre :
// ::1 appartient à ::/96 et se réduit en 0.0.0.1, qui n'est ni la boucle locale
// ni l'adresse non spécifiée — seul « this network » le rattrape. Une adresse est
// donc interdite si l'une ou l'autre de ses deux formes l'est.
func adresseInterdite(a netip.Addr) bool {
	return interdite(a.Unmap()) || interdite(reduite(a))
}

func interdite(a netip.Addr) bool {
	if !a.IsValid() ||
		a.IsUnspecified() ||
		a.IsLoopback() ||
		a.IsPrivate() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() ||
		a.IsMulticast() {
		return true
	}
	for _, plage := range plagesReservees {
		if plage.Contains(a) {
			return true
		}
	}
	return false
}

func resolutionSysteme(ctx context.Context, hote string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", hote)
}

// verifieRedirection compte les sauts et revérifie le schéma de chacun. La
// politique d'IP, elle, s'applique plus bas, au connect.
func (r *recuperateur) verifieRedirection(req *http.Request, via []*http.Request) error {
	if len(via) > redirectionsMax {
		r.refus = &Erreur{Cause: Injoignable, URL: req.URL.String()}
		return errRedirectionArretee
	}
	if _, err := analyseURL(req.URL.String()); err != nil {
		r.refus = &Erreur{Cause: RefuseeParPolitique, URL: req.URL.String()}
		return errRedirectionArretee
	}
	return nil
}

// demande envoie une requête sous notre nom.
func (r *recuperateur) demande(ctx context.Context, adresse string) (*http.Response, error) {
	r.refus = nil
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adresse, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", Agent)
	return r.client.Do(req)
}

// echec nomme la cause d'une erreur de transport. siDelai dit ce que vaut un
// dépassement de délai à cet endroit-là : sur la page il se nomme, sur
// robots.txt il ne fait qu'empêcher d'atteindre le site.
func (r *recuperateur) echec(err error, adresse, siDelai string) *Erreur {
	if r.refus != nil {
		return r.refus
	}

	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		adresse = ue.URL
	}

	switch {
	case errors.Is(err, errAdresseRefusee):
		return &Erreur{Cause: RefuseeParPolitique, URL: adresse}
	case errors.Is(err, context.DeadlineExceeded), estDelai(err):
		return &Erreur{Cause: siDelai, URL: adresse}
	default:
		return &Erreur{Cause: Injoignable, URL: adresse}
	}
}

func estDelai(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// page va chercher la page elle-même.
func (r *recuperateur) page(ctx context.Context, cible *url.URL) (Page, error) {
	ctx, arrete, err := r.echange(ctx, hoteDe(cible))
	if err != nil {
		return Page{}, r.echec(err, cible.String(), DelaiDepasse)
	}
	defer arrete()

	reponse, err := r.demande(ctx, cible.String())
	if err != nil {
		return Page{}, r.echec(err, cible.String(), DelaiDepasse)
	}
	defer reponse.Body.Close()

	finale := cible.String()
	if reponse.Request != nil {
		finale = reponse.Request.URL.String()
	}
	if reponse.StatusCode < 200 || reponse.StatusCode > 299 {
		return Page{}, &Erreur{Cause: RefusHTTP, Code: reponse.StatusCode, URL: finale}
	}

	// Un octet de plus que le plafond suffit à savoir qu'il est dépassé, et
	// évite de charger en mémoire une page qui ne s'arrête jamais.
	corps, err := io.ReadAll(io.LimitReader(reponse.Body, r.o.tailleMax+1))
	if err != nil {
		return Page{}, r.echec(err, finale, DelaiDepasse)
	}
	if int64(len(corps)) > r.o.tailleMax {
		return Page{}, &Erreur{Cause: TailleMax, URL: finale}
	}

	return Page{
		Corps:       corps,
		URLFinale:   finale,
		TypeContenu: reponse.Header.Get("Content-Type"),
	}, nil
}

// robotsInterdit demande le robots.txt de l'hôte, sous les mêmes règles que la
// page, et l'applique au chemin demandé. Il rend nil quand rien ne s'y oppose.
//
// Une redirection qui change d'hôte ne le fait pas redemander : c'est le site
// qui redirige, et l'utilisateur a demandé l'URL initiale.
func (r *recuperateur) robotsInterdit(ctx context.Context, cible *url.URL) *Erreur {
	adresse := (&url.URL{Scheme: cible.Scheme, Host: cible.Host, Path: "/robots.txt"}).String()
	hote := hoteDe(cible)

	dit, refus := r.robotsDe(ctx, adresse, hote)
	if refus != nil {
		return refus
	}
	if dit.interditTout || !dit.regles.autorise(chemin(cible), Agent) {
		return &Erreur{Cause: RobotsInterdit, URL: cible.String()}
	}

	// Rapporté même quand rien n'est interdit : c'est le cas ordinaire, et c'est
	// justement là qu'un appelant qui enchaîne en a besoin. Rapporté maintenant,
	// avant la requête de page : appris puis rapporté après coup, le Crawl-delay
	// ne vaudrait qu'à partir de la page suivante, et celle-ci partirait à notre
	// rythme et non à celui du site, qui vient pourtant de l'écrire.
	if r.o.cadence != nil {
		r.o.cadence.Retiens(hote, dit.regles.delaiPour(Agent))
	}
	return nil
}

// robotsDe rend ce que le robots.txt de l'hôte dit, du cache s'il y est déjà.
//
// Ce qui se garde est la décision, pas la réponse : elle ne dépend plus du
// chemin demandé, et c'est ce qui permet de la partager entre toutes les pages
// d'un même hôte.
func (r *recuperateur) robotsDe(ctx context.Context, adresse, hote string) (decisionRobots, *Erreur) {
	if dit, vu := r.o.robots.lis(adresse); vu {
		return dit, nil
	}

	dit, refus := r.demandeRobots(ctx, adresse, hote)
	if refus != nil {
		return decisionRobots{}, refus
	}
	// Le refus né d'un serveur en panne ne se garde pas. Il est daté, pas
	// définitif : gardé, une panne d'une seconde condamnerait l'hôte entier en
	// robots_interdit — sort définitif que rien ne rejoue — pour toute la durée
	// de la fournée. Ce qui se garde est ce que le site a dit de lui-même : ses
	// règles, ou l'absence de robots.txt.
	if !dit.interditTout {
		r.o.robots.garde(adresse, dit)
	}
	return dit, nil
}

// demandeRobots va chercher le robots.txt et en tire ce qu'il dit de l'hôte.
func (r *recuperateur) demandeRobots(ctx context.Context, adresse, hote string) (decisionRobots, *Erreur) {
	ctx, arrete, err := r.echange(ctx, hote)
	if err != nil {
		return decisionRobots{}, r.echec(err, adresse, Injoignable)
	}
	defer arrete()

	reponse, err := r.demande(ctx, adresse)
	if err != nil {
		return decisionRobots{}, r.echec(err, adresse, Injoignable)
	}
	defer reponse.Body.Close()

	switch {
	case reponse.StatusCode >= 500:
		// Le REP demande de s'abstenir quand le serveur est en panne : une
		// erreur n'est pas une autorisation. Le 403 anti-robot, lui, tombe du
		// côté « autorisé », et c'est voulu.
		return decisionRobots{interditTout: true}, nil
	case reponse.StatusCode != http.StatusOK:
		return decisionRobots{}, nil
	}

	texte, err := io.ReadAll(io.LimitReader(reponse.Body, r.o.tailleMax))
	if err != nil {
		return decisionRobots{}, r.echec(err, adresse, Injoignable)
	}
	return decisionRobots{regles: analyseRobots(string(texte))}, nil
}

// hoteDe rend l'hôte d'une cible, en minuscules : c'est la clé de la cadence,
// et Example.com est le même site qu'example.com.
func hoteDe(u *url.URL) string {
	return strings.ToLower(u.Hostname())
}

// chemin rend le chemin tel que robots.txt le compare.
func chemin(u *url.URL) string {
	c := u.EscapedPath()
	if c == "" {
		return "/"
	}
	if !strings.HasPrefix(c, "/") {
		return "/" + c
	}
	return c
}
