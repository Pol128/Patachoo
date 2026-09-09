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
	// DelaiMaxDefaut borne l'échange entier — robots.txt compris.
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
	// DelaiAnnonce est le Crawl-delay que le robots.txt de l'hôte demande, ou
	// zéro s'il n'en demande pas. Ce paquet le lit et ne l'applique pas : il va
	// chercher une page, il n'en enchaîne pas. C'est l'appelant qui enchaîne
	// — l'import en lot — qui espace ses requêtes de ce que l'hôte réclame.
	DelaiAnnonce time.Duration
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

// Cadence est le rythme que l'appelant impose aux requêtes que ce paquet émet,
// hôte par hôte.
//
// Un appel en émet deux : le robots.txt de l'hôte, que l'appelant n'a pas
// demandé, puis la page. Sans cadence elles partent collées — un appelant qui
// espace ses appels d'une seconde en envoie tout de même deux dans le même
// instant, et sa politesse ne vaut que pour la couture qu'il tient. Avec,
// chacune attend son tour, robots.txt compris.
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
func AvecCadence(c Cadence) Option {
	return func(o *options) { o.cadence = c }
}

// AvecRobotsRetenus garde le robots.txt de chaque hôte au lieu de le
// redemander à chaque page.
//
// Une fournée de vingt pages sur un même site lui coûte alors vingt-et-une
// requêtes et non quarante. Le cache appartient à l'appelant : c'est lui qui
// décide de sa durée de vie — celle d'une fournée —, et rien ici ne le
// périme.
func AvecRobotsRetenus(r *RobotsRetenus) Option {
	return func(o *options) { o.robots = r }
}

// Option règle un appel.
type Option func(*options)

// AvecDelaiMax borne l'échange entier, robots.txt compris.
func AvecDelaiMax(d time.Duration) Option {
	return func(o *options) { o.delaiMax = d }
}

// AvecTailleMax borne le corps lu.
func AvecTailleMax(octets int64) Option {
	return func(o *options) { o.tailleMax = octets }
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

	ctx, arrete := context.WithTimeout(ctx, o.delaiMax)
	defer arrete()

	r := &recuperateur{o: o}
	r.client = &http.Client{Transport: r.pile(), CheckRedirect: r.verifieRedirection}

	if refus := r.robotsInterdit(ctx, cible); refus != nil {
		return Page{}, refus
	}
	return r.page(ctx, cible)
}

// tourDeRole fait attendre son tour à une requête avant de l'émettre. C'est le
// seul endroit où la cadence s'applique, et il est sous tout ce qui émet.
type tourDeRole struct {
	suivant http.RoundTripper
	cadence Cadence
}

func (t tourDeRole) RoundTrip(requete *http.Request) (*http.Response, error) {
	if err := t.cadence.AttendSonTour(requete.Context(), strings.ToLower(requete.URL.Hostname())); err != nil {
		return nil, err
	}
	return t.suivant.RoundTrip(requete)
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
	// delaiAnnonce est retenu à la lecture du robots.txt, et reporté sur la
	// page rendue.
	delaiAnnonce time.Duration
}

// pile monte le transport HTTP. Le proxy est retiré : un proxy déclaré dans
// l'environnement ferait porter la politique d'IP sur lui plutôt que sur la
// vraie destination.
func (r *recuperateur) pile() http.RoundTripper {
	return r.cadence(r.transportNu())
}

// cadence enveloppe le transport pour que chaque requête attende son tour.
//
// À ce niveau-ci et non dans Recupere : c'est le seul endroit par lequel toutes
// les requêtes passent — le robots.txt, la page, et chaque saut d'une
// redirection. Une attente posée plus haut laisserait toujours partir deux
// requêtes dans le même instant.
func (r *recuperateur) cadence(suivant http.RoundTripper) http.RoundTripper {
	if r.o.cadence == nil {
		return suivant
	}
	return tourDeRole{suivant: suivant, cadence: r.o.cadence}
}

func (r *recuperateur) transportNu() http.RoundTripper {
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

// adresseInterdite dit les adresses que nous n'allons pas chercher : la boucle
// locale, les plages privées, le lien-local — dont 169.254.169.254, qui sert les
// métadonnées des hébergeurs — le multicast et l'adresse non spécifiée.
//
// La forme mappée ::ffff:127.0.0.1 est ramenée à sa forme v4 avant l'examen :
// sans quoi elle passerait pour une adresse v6 quelconque.
func adresseInterdite(a netip.Addr) bool {
	a = a.Unmap()
	return !a.IsValid() ||
		a.IsUnspecified() ||
		a.IsLoopback() ||
		a.IsPrivate() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() ||
		a.IsMulticast()
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
		Corps:        corps,
		URLFinale:    finale,
		TypeContenu:  reponse.Header.Get("Content-Type"),
		DelaiAnnonce: r.delaiAnnonce,
	}, nil
}

// robotsInterdit demande le robots.txt de l'hôte, sous les mêmes règles que la
// page, et l'applique au chemin demandé. Il rend nil quand rien ne s'y oppose.
//
// Une redirection qui change d'hôte ne le fait pas redemander : c'est le site
// qui redirige, et l'utilisateur a demandé l'URL initiale.
func (r *recuperateur) robotsInterdit(ctx context.Context, cible *url.URL) *Erreur {
	adresse := (&url.URL{Scheme: cible.Scheme, Host: cible.Host, Path: "/robots.txt"}).String()

	dit, refus := r.robotsDe(ctx, adresse)
	if refus != nil {
		return refus
	}
	if dit.interditTout || !dit.regles.autorise(chemin(cible), Agent) {
		return &Erreur{Cause: RobotsInterdit, URL: cible.String()}
	}

	// Retenu même quand rien n'est interdit : c'est le cas ordinaire, et c'est
	// justement là qu'un appelant qui enchaîne en a besoin.
	r.delaiAnnonce = dit.regles.delaiPour(Agent)
	// Rapporté maintenant, avant la requête de page : un Crawl-delay appris
	// puis rapporté après coup ne vaudrait qu'à partir de la page suivante, et
	// celle-ci partirait à notre rythme et non à celui du site.
	if r.o.cadence != nil {
		r.o.cadence.Retiens(strings.ToLower(cible.Hostname()), r.delaiAnnonce)
	}
	return nil
}

// robotsDe rend ce que le robots.txt de l'hôte dit, du cache s'il y est déjà.
//
// Ce qui se garde est la décision, pas la réponse : elle ne dépend plus du
// chemin demandé, et c'est ce qui permet de la partager entre toutes les pages
// d'un même hôte. Un robots.txt qui n'a pas été atteint, lui, ne se garde pas —
// une panne de réseau passagère condamnerait tout l'hôte pour la durée de la
// fournée.
func (r *recuperateur) robotsDe(ctx context.Context, adresse string) (decisionRobots, *Erreur) {
	if dit, vu := r.o.robots.lis(adresse); vu {
		return dit, nil
	}

	dit, refus := r.demandeRobots(ctx, adresse)
	if refus != nil {
		return decisionRobots{}, refus
	}
	r.o.robots.garde(adresse, dit)
	return dit, nil
}

// demandeRobots va chercher le robots.txt et en tire ce qu'il dit de l'hôte.
func (r *recuperateur) demandeRobots(ctx context.Context, adresse string) (decisionRobots, *Erreur) {
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
