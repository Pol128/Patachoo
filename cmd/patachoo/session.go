package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"github.com/pocketbase/pocketbase/tools/security"
)

// nomCookieSession : un nom à nous, et non le pb_auth du SDK JavaScript de
// PocketBase. Celui-ci porte un objet JSON — jeton et enregistrement — quand
// nous n'y mettons que le jeton nu ; réutiliser son nom promettrait une
// compatibilité que ce cookie n'a pas.
//
// Le préfixe __Host- n'est pas décoratif : le navigateur refuse d'enregistrer
// un cookie ainsi nommé s'il ne vient pas d'une origine sûre, s'il porte un
// Domain, ou si son Path n'est pas « / ». Sans lui, un voisin qui partage
// notre domaine enregistrable — blog.exemple.fr à côté de patachoo.exemple.fr
// — pose ce même nom avec son propre jeton et Domain=exemple.fr, et le
// navigateur envoie deux cookies dont rien ici ne peut distinguer l'origine :
// la victime se retrouve connectée au compte de l'attaquant. Le préfixe ferme
// cette porte du côté du navigateur, à condition que cookieDeSession continue
// de poser Secure et Path=/ sans jamais poser de Domain — les trois attributs
// dont il dépend, et que attributsDeSession garde dans les tests.
const nomCookieSession = "__Host-patachoo_session"

// nomCookieOuvertureDeSession date l'ouverture de la session, et lui seul : le
// jeton de PocketBase ne porte que son échéance (core/record_tokens.go), pas sa
// date de naissance, et chaque renouvellement la repousse de cinq jours pleins.
// Sans ce second cookie, une session obtenue par renouvellements successifs ne
// finit jamais : un jeton capté une fois ouvre le compte tant que son porteur
// émet une requête par demi-vie.
//
// Le préfixe __Host- pour les mêmes raisons que nomCookieSession, et il compte
// autant ici : un voisin qui partage notre domaine enregistrable poserait
// sinon ce nom-là avec sa propre date d'ouverture, et rendrait au jeton volé la
// durée que ce cookie lui retire.
const nomCookieOuvertureDeSession = "__Host-patachoo_session_ouverte"

// dureeMaximaleDeSession borne la durée totale d'une session, renouvellements
// compris. Passé ce délai, le renouvellement cesse : la session s'éteint à
// l'échéance de son jeton courant, et le compte doit se reconnecter.
//
// Trente jours, tranché le 16/09/2026. Sept faisait payer une reconnexion
// hebdomadaire à l'usage, quatre-vingt-dix laissait un trimestre à un jeton
// volé. Le dernier renouvellement possible tombe donc au plus tard au trentième
// jour, et la reconnexion est exigée entre le 30e et le 35e — un jeton dure
// cinq jours.
const dureeMaximaleDeSession = 30 * 24 * time.Hour

// typeJetonOuvertureDeSession sépare le cookie d'ouverture du jeton
// d'authentification, que la même clé signe.
//
// Sans cette revendication, le jeton volé serait à lui-même son propre cookie
// d'ouverture : recopié sous l'autre nom, il se vérifierait avec la même clé et
// porterait déjà le bon identifiant de compte, pour une échéance à cinq jours
// toujours fraîche. Le plafond ne bornerait plus rien.
//
// Une valeur à nous, et non l'une des cinq de core.TokenType* : ce jeton n'est
// pas de PocketBase, et se ranger dans sa nomenclature promettrait une parenté
// qu'il n'a pas.
const typeJetonOuvertureDeSession = "patachooSessionOuverte"

// cleSessionPorteeParLeCookie marque, dans le magasin de la requête, que c'est
// notre cookie qui a fourni le jeton d'authentification — et non un en-tête que
// le client portait déjà. Un marqueur plutôt qu'une relecture du cookie : la
// présence d'un cookie ne dit pas qu'il a authentifié quoi que ce soit.
const cleSessionPorteeParLeCookie = "patachooSessionPorteeParLeCookie"

// Les deux priorités qui font tout tenir. Les handlers sont triés par priorité
// croissante (tools/hook/hook.go), donc :
//
//   - la recopie du cookie passe avant pbLoadAuthToken, qui ne lit le jeton que
//     dans l'en-tête Authorization (apis/middlewares.go, getAuthTokenFromRequest)
//     et qu'un navigateur demandant une page HTML n'envoie jamais ;
//   - le renouvellement passe après, puisqu'il lui faut e.Auth peuplé, et avant
//     que le gestionnaire n'écrive la réponse : un Set-Cookie posé après le
//     corps ne partirait pas.
//
// Un cran d'écart, et non dix. Dix tombait par hasard sur deux priorités que
// PocketBase déclare — le recouvrement de panique en dessous, les en-têtes de
// sécurité au-dessus — et le tri préserve l'ordre d'enregistrement à égalité
// (tools/hook/hook.go) : l'ordre d'exécution ne tenait plus qu'à celui du
// branchement, que rien ici ne fixe. Un cran dit en outre ce qu'on veut
// vraiment, c'est-à-dire coller au chargement du jeton : plus l'écart est
// petit, moins il reste de place pour qu'un middleware tiers vienne s'y
// glisser.
const (
	prioriteRecopieDuCookie = apis.DefaultLoadAuthTokenMiddlewarePriority - 1
	prioriteRenouvellement  = apis.DefaultLoadAuthTokenMiddlewarePriority + 1
)

// brancheLaSession pose les deux middlewares de session sur le routeur.
func brancheLaSession(routeur *router.Router[*core.RequestEvent]) {
	routeur.Bind(recopieLeCookieDansLEnTete())
	routeur.Bind(renouvelleLaSession())
}

// prefixesDePocketBase : les deux racines que PocketBase enregistre pour
// lui-même, et les seules — /api/… (apis/base.go, Group("/api")) et /_/…
// (apis/serve.go, GET /_/{path...} ; apis/extensions.go, Group("/_")).
//
// La barre finale est portée par le préfixe, et non déduite : sans elle,
// /apiculture serait tenu pour une route de l'API.
var prefixesDePocketBase = []string{"/api/", "/_/"}

// recopieLeCookieDansLEnTete rend la session lisible par PocketBase.
//
// Un en-tête Authorization déjà présent l'emporte : un client d'API qui porte
// son propre jeton n'a pas à être supplanté par un cookie que le navigateur
// aurait joint à la requête.
//
// La recopie s'arrête aux chemins du produit. Elle est posée sur le routeur
// entier, donc en amont des routes que PocketBase branche derrière notre
// se.Next() : sans cette garde, elle écrivait aussi le jeton du cookie sur
// /api/ et /_/, où getAuthTokenFromRequest (apis/middlewares.go) le lit tel
// quel — PocketBase n'exige pas le préfixe Bearer. Un fetch("/api/…",
// {credentials:"same-origin"}) depuis n'importe quelle page partait alors
// authentifié comme le compte connecté, sans que le navigateur ait eu à lire
// le cookie : HttpOnly n'y change rien, c'est le serveur qui faisait la
// recopie. Toute XSS dans une page obtenait ainsi l'API REST complète du
// compte — la lecture de collections entières que nos pages ne montrent
// jamais, les opérations de compte, /api/files/ — là où elle n'avait que les
// formulaires du produit.
//
// /_/ y figure alors qu'un jeton users n'y donne rien : l'interface
// d'administration exige un jeton de la collection _superusers, et /connexion
// n'émet que des jetons users. Ce que la garde refuse là n'est donc pas un
// accès, c'est de faire reposer une session de navigateur sur une requête
// d'administration — la règle porte sur le chemin, pas sur ce que le chemin
// sert, et elle vaudra encore le jour où /_/ servira autre chose.
//
// Le marqueur compte autant que l'en-tête : laissé posé, il ferait redéposer
// par renouvelleLaSession un cookie de session de cinq jours sur la réponse
// d'une requête d'API, que le navigateur n'a pas demandée.
//
// Un simple préfixe sur URL.Path suffit, sans normalisation à écrire : les
// middlewares du routeur ne s'exécutent qu'après l'appariement par le
// http.ServeMux (tools/router/router.go, loadMux), sur un chemin déjà nettoyé
// — //api/… et /api/../… partent en redirection avant d'atteindre le
// gestionnaire — et déjà déséchappé, URL.Path portant la forme décodée.
func recopieLeCookieDansLEnTete() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooSessionDepuisCookie",
		Priority: prioriteRecopieDuCookie,
		Func: func(e *core.RequestEvent) error {
			if !estUnCheminDePocketBase(e.Request.URL.Path) && e.Request.Header.Get("Authorization") == "" {
				if cookie, err := e.Request.Cookie(nomCookieSession); err == nil && cookie.Value != "" {
					e.Request.Header.Set("Authorization", cookie.Value)
					e.Set(cleSessionPorteeParLeCookie, cookie.Value)
				}
			}
			return e.Next()
		},
	}
}

// estUnCheminDePocketBase dit si le chemin appartient à l'API REST ou à
// l'interface d'administration, plutôt qu'aux pages du produit.
func estUnCheminDePocketBase(chemin string) bool {
	for _, prefixe := range prefixesDePocketBase {
		if strings.HasPrefix(chemin, prefixe) {
			return true
		}
	}
	return false
}

// renouvelleLaSession redépose un cookie quand le jeton a passé la mi-vie.
//
// Sans lui, toute session meurt sèchement au bout de sa durée de vie, y compris
// en pleine saisie. Un jeton illisible, absent ou encore jeune ne produit rien :
// la requête se poursuit, c'est pbLoadAuthToken qui a déjà tranché de sa
// validité.
func renouvelleLaSession() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooRenouvellementSession",
		Priority: prioriteRenouvellement,
		Func: func(e *core.RequestEvent) error {
			if jeton, duree, ok := sessionARenouveler(e); ok {
				poseLeCookieDeSession(e, cookieDeSession(jeton, duree))
			}
			return e.Next()
		},
	}
}

// sessionARenouveler dit s'il faut réémettre un jeton, et lequel.
func sessionARenouveler(e *core.RequestEvent) (string, time.Duration, bool) {
	if e.Auth == nil {
		return "", 0, false
	}

	// Seule une session portée par notre cookie se renouvelle, et c'est le
	// marqueur qui le dit — pas la présence d'un cookie. Une requête peut
	// porter les deux : le cookie du compte A et l'en-tête Authorization du
	// compte B. L'en-tête l'emporte alors, e.Auth est B, et décider sur le
	// jeton de A pour réémettre celui de B reposerait la session du navigateur
	// sur un compte que son cookie ne désignait pas.
	jetonDuCookie, porteeParLeCookie := e.Get(cleSessionPorteeParLeCookie).(string)
	if !porteeParLeCookie || jetonDuCookie == "" {
		return "", 0, false
	}

	// Un jeton non renouvelable garde la borne choisie à son émission : c'est
	// le cas des jetons statiques de l'impersonation, émis pour quelques
	// minutes. Le remplacer par un jeton ordinaire de cinq jours effacerait
	// cette borne, puis la prolongerait de renouvellement en renouvellement.
	// PocketBase applique la même règle sur sa propre route de renouvellement
	// (apis/record_auth_refresh.go).
	if !estRenouvelable(jetonDuCookie) {
		return "", 0, false
	}

	duree := e.Auth.Collection().AuthToken.DurationTime()
	if duree <= 0 || !aPasseLaMiVie(jetonDuCookie, duree) {
		return "", 0, false
	}

	// La borne de durée totale, ici et pas ailleurs : après la mi-vie, qui se
	// lit dans le jeton, et avant la règle d'authentification, qui se lit en
	// base. Le cookie d'ouverture se lit en mémoire comme le reste ; le cas
	// rare est le seul qui doive payer la base.
	if !lOuvertureTientDansLaBorne(e) {
		return "", 0, false
	}

	// La règle d'authentification décide de la prolongation comme elle décide
	// de l'ouverture. Sans cette lecture, un compte que la règle ne couvre plus
	// garde sa session indéfiniment tant qu'il émet une requête par demi-vie :
	// il ne se reconnecte jamais, donc laConnexionEstAutorisee ne l'atteint
	// plus, et la borne que son commentaire invoque comme parade — « un jeton
	// émis en la violant resterait valable jusqu'à son échéance » — ne tombe
	// plus jamais. PocketBase rejoue la règle sur sa propre route de
	// renouvellement (apis/record_auth_refresh.go → recordAuthResponse).
	//
	// Ici et pas plus haut : la règle s'évalue en base, quand tout ce qui
	// précède se lit dans le jeton. Une session sous la mi-vie est le cas rare,
	// et c'est le seul qui doive payer ce coût.
	if !laRegleDAuthentificationAutorise(e, e.Auth) {
		return "", 0, false
	}

	jeton, err := e.Auth.NewAuthToken()
	if err != nil {
		e.App.Logger().Error("renouvellement de la session impossible", "erreur", err)
		return "", 0, false
	}
	return jeton, duree, true
}

// estRenouvelable lit la revendication que PocketBase pose à l'émission :
// vraie sur un jeton ordinaire (NewAuthToken), fausse sur un jeton statique
// (NewStaticAuthToken). Absente, ou d'un autre type que le booléen JSON qu'y
// écrit core/record_tokens.go, elle vaut refus — un jeton dont on ne sait rien
// ne se prolonge pas.
//
// La signature n'est pas revérifiée ici, pour la même raison que dans
// aPasseLaMiVie.
func estRenouvelable(jeton string) bool {
	revendications, err := security.ParseUnverifiedJWT(jeton)
	if err != nil {
		return false
	}

	renouvelable, estUnBooleen := revendications[core.TokenClaimRefreshable].(bool)
	return estUnBooleen && renouvelable
}

// aPasseLaMiVie dit si le jeton a consommé plus de la moitié de sa vie.
//
// La signature n'est pas revérifiée ici — « unverified » est sans danger,
// puisque FindAuthRecordByToken l'a validée juste avant, dans pbLoadAuthToken.
func aPasseLaMiVie(jeton string, duree time.Duration) bool {
	revendications, err := security.ParseUnverifiedJWT(jeton)
	if err != nil {
		return false
	}

	echeance, err := revendications.GetExpirationTime()
	if err != nil || echeance == nil {
		return false
	}

	return time.Until(echeance.Time) < duree/2
}

// cookieDeSession porte le jeton au navigateur.
//
// SameSite=Lax, et pas Strict : Strict casse le retour depuis une redirection
// externe, donc la connexion par fournisseur tiers à venir. C'est aussi notre
// seule défense CSRF sur les formulaires POST tant qu'aucun jeton anti-rejeu
// n'existe — le passer à None un jour de débogage ouvrirait cette porte-là.
//
// Secure sans condition sur le schéma : les navigateurs traitent
// http://localhost comme une origine sûre et y acceptent les cookies Secure. Un
// drapeau conditionnel serait une branche de sécurité qui ne s'exécute jamais.
//
// Max-Age suit la durée de vie du jeton et n'est pas recopié en dur : un cookie
// qui survit à son jeton produit une session fantôme, et l'inverse une
// déconnexion inexpliquée.
func cookieDeSession(jeton string, duree time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     nomCookieSession,
		Value:    jeton,
		Path:     "/",
		MaxAge:   int(duree.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// poseLeCookieDeSession écrit le cookie de session.
func poseLeCookieDeSession(e *core.RequestEvent, cookie *http.Cookie) {
	poseLeCookie(e, cookie)
}

// poseLeCookie écrit un cookie en retirant d'abord celui qu'une étape
// antérieure aurait déjà posé sous le même nom.
//
// http.SetCookie ajoute un en-tête au lieu de le remplacer. Sans ce ménage,
// une déconnexion faite sous la mi-vie part avec deux Set-Cookie de même nom
// — le jeton frais du renouvellement, qui s'exécute avant le gestionnaire,
// puis l'effacement. Un navigateur applique le dernier, mais la réponse qui
// révoque une session y transporte quand même un jeton vivant, et tout ce qui
// lit le premier reste connecté.
//
// Le nom vient du cookie donné, et n'est plus celui de la session en dur : le
// jeton anti-rejeu se pose sur la même réponse et tombe dans le même piège,
// et un ménage qui filtrerait sur le seul nom de la session emporterait l'un
// en reposant l'autre.
func poseLeCookie(e *core.RequestEvent, cookie *http.Cookie) {
	entetes := e.Response.Header()

	gardes := make([]string, 0, len(entetes.Values("Set-Cookie")))
	for _, pose := range entetes.Values("Set-Cookie") {
		if !strings.HasPrefix(pose, cookie.Name+"=") {
			gardes = append(gardes, pose)
		}
	}

	entetes.Del("Set-Cookie")
	for _, pose := range gardes {
		entetes.Add("Set-Cookie", pose)
	}
	e.SetCookie(cookie)
}

// cookieDeSessionEfface ordonne l'oubli du cookie.
//
// Mêmes Path et attributs que celui qu'il remplace : un cookie d'effacement qui
// diffère par un seul d'entre eux laisse l'original en place, à côté.
func cookieDeSessionEfface() *http.Cookie {
	efface := cookieDeSession("", 0)
	efface.MaxAge = -1
	return efface
}

// revendicationsDOuverture dit de qui la session est, et rien de plus.
//
// La date d'ouverture, elle, n'a pas de revendication à elle : security.NewJWT
// pose l'échéance à l'instant plus la durée qu'on lui donne, et cette durée est
// le plafond lui-même — l'ouverture est donc l'exp moins le plafond. Une
// seconde revendication qui la porterait pourrait contredire la première, et il
// faudrait alors décider laquelle fait foi.
func revendicationsDOuverture(compte *core.Record) jwt.MapClaims {
	return jwt.MapClaims{
		core.TokenClaimType: typeJetonOuvertureDeSession,
		core.TokenClaimId:   compte.Id,
	}
}

// cleDOuvertureDeSession est celle qui signe déjà les jetons du compte
// (core/record_tokens.go).
//
// La réemployer donne deux propriétés sans une ligne de plus : le cookie meurt
// quand le mot de passe ou le courriel change, puisque PocketBase régénère
// alors tokenKey, et il ne vaut que pour le compte qui l'a reçu. Une clé à
// nous, tirée ailleurs, aurait survécu au changement de mot de passe — c'est-à-dire
// au seul geste qui coupe tout.
func cleDOuvertureDeSession(compte *core.Record) string {
	return compte.TokenKey() + compte.Collection().AuthToken.Secret
}

// cookieDOuvertureDeSession date l'ouverture au navigateur.
//
// Mêmes attributs que cookieDeSession, jusqu'au Path : deux cookies de la même
// session qui différeraient d'un seul d'entre eux, et le navigateur en garde
// deux homonymes dont l'un ne s'efface jamais. Max-Age vaut le plafond, comme
// l'exp du jeton qu'il porte : un cookie qui lui survivrait ne prouverait plus
// rien, et l'inverse couperait la session avant sa borne.
func cookieDOuvertureDeSession(compte *core.Record) (*http.Cookie, error) {
	jeton, err := security.NewJWT(revendicationsDOuverture(compte), cleDOuvertureDeSession(compte), dureeMaximaleDeSession)
	if err != nil {
		return nil, err
	}

	return &http.Cookie{
		Name:     nomCookieOuvertureDeSession,
		Value:    jeton,
		Path:     "/",
		MaxAge:   int(dureeMaximaleDeSession.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

// poseLOuvertureDeSession dépose le cookie qui date la session, aux deux seuls
// endroits où une session naît — et jamais au renouvellement, qui est toute sa
// raison d'être : redaté à chaque passage, le plafond se remettrait à zéro et
// ne bornerait rien.
func poseLOuvertureDeSession(e *core.RequestEvent, compte *core.Record) error {
	cookie, err := cookieDOuvertureDeSession(compte)
	if err != nil {
		return err
	}
	poseLeCookie(e, cookie)
	return nil
}

// cookieDOuvertureEfface ordonne l'oubli du cookie d'ouverture.
func cookieDOuvertureEfface() *http.Cookie {
	efface := &http.Cookie{
		Name:     nomCookieOuvertureDeSession,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	return efface
}

// lOuvertureTientDansLaBorne dit si la session peut encore être prolongée.
//
// Absent vaut refus, jamais « session neuve » : traiter l'absence comme une
// ouverture à l'instant rendrait le plafond décoratif, puisqu'il suffirait de
// jeter le cookie pour le remettre à zéro. La conséquence est assumée — les
// sessions ouvertes avant le déploiement n'ont pas ce cookie, cessent de se
// renouveler et s'éteignent dans les cinq jours.
//
// La signature est vérifiée ici, à la différence d'aPasseLaMiVie : personne ne
// l'a validée avant nous, ce cookie n'étant pas celui que PocketBase lit. C'est
// elle qui fait tout le travail — sans elle, il suffirait de récrire la date.
//
// L'identifiant est confronté à celui du compte alors que la clé de signature
// est déjà propre au compte : le contrôle est redondant aujourd'hui, et il ne
// le serait plus le jour où la clé cesserait de l'être.
func lOuvertureTientDansLaBorne(e *core.RequestEvent) bool {
	cookie, err := e.Request.Cookie(nomCookieOuvertureDeSession)
	if err != nil {
		return false
	}

	// ParseJWT refuse de lui-même un jeton expiré : la borne dépassée y entre
	// par le même chemin qu'une signature qui ne vérifie pas.
	revendications, err := security.ParseJWT(cookie.Value, cleDOuvertureDeSession(e.Auth))
	if err != nil {
		return false
	}

	if typeDuJeton, _ := revendications[core.TokenClaimType].(string); typeDuJeton != typeJetonOuvertureDeSession {
		return false
	}

	id, _ := revendications[core.TokenClaimId].(string)
	return id == e.Auth.Id
}

// utilisateur porte ce que les gabarits ont le droit de connaître du compte
// connecté : de quoi l'afficher, et rien de plus.
//
// Rien de plus, donc pas d'identifiant : aucun gabarit ne le lit, et un champ
// qu'on renseigne sans l'employer finit par être lu comme une permission — la
// page aurait le droit de désigner un compte, alors qu'elle n'a que celui de le
// nommer. Il se rajoutera le jour où une page en aura besoin, avec cette
// page-là.
type utilisateur struct {
	Nom string
}

// utilisateurCourant traduit e.Auth pour les gabarits, ou rend nil pour un
// visiteur. Le nom retombe sur le courriel : un compte créé sans nom doit
// quand même s'afficher dans l'en-tête.
func utilisateurCourant(e *core.RequestEvent) *utilisateur {
	if e.Auth == nil {
		return nil
	}

	nom := strings.TrimSpace(e.Auth.GetString("name"))
	if nom == "" {
		nom = e.Auth.Email()
	}
	return &utilisateur{Nom: nom}
}
