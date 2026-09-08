package main

import (
	"net/http"
	"strings"
	"time"

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
const nomCookieSession = "patachoo_session"

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

// recopieLeCookieDansLEnTete rend la session lisible par PocketBase.
//
// Un en-tête Authorization déjà présent l'emporte : un client d'API qui porte
// son propre jeton n'a pas à être supplanté par un cookie que le navigateur
// aurait joint à la requête.
func recopieLeCookieDansLEnTete() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooSessionDepuisCookie",
		Priority: prioriteRecopieDuCookie,
		Func: func(e *core.RequestEvent) error {
			if e.Request.Header.Get("Authorization") == "" {
				if cookie, err := e.Request.Cookie(nomCookieSession); err == nil && cookie.Value != "" {
					e.Request.Header.Set("Authorization", cookie.Value)
					e.Set(cleSessionPorteeParLeCookie, cookie.Value)
				}
			}
			return e.Next()
		},
	}
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

// poseLeCookieDeSession écrit le cookie en retirant d'abord celui qu'une
// étape antérieure aurait déjà posé.
//
// http.SetCookie ajoute un en-tête au lieu de le remplacer. Sans ce ménage,
// une déconnexion faite sous la mi-vie part avec deux Set-Cookie de même nom
// — le jeton frais du renouvellement, qui s'exécute avant le gestionnaire,
// puis l'effacement. Un navigateur applique le dernier, mais la réponse qui
// révoque une session y transporte quand même un jeton vivant, et tout ce qui
// lit le premier reste connecté.
func poseLeCookieDeSession(e *core.RequestEvent, cookie *http.Cookie) {
	entetes := e.Response.Header()

	gardes := make([]string, 0, len(entetes.Values("Set-Cookie")))
	for _, pose := range entetes.Values("Set-Cookie") {
		if !strings.HasPrefix(pose, nomCookieSession+"=") {
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
// visiteur.
func utilisateurCourant(e *core.RequestEvent) *utilisateur {
	if e.Auth == nil {
		return nil
	}
	return &utilisateur{Nom: nomAffichable(e.Auth)}
}

// nomAffichable rend de quoi désigner un compte dans une page.
//
// Le nom retombe sur le courriel : le champ name n'est pas requis sur users, et
// un compte créé sans nom doit quand même s'afficher — dans l'en-tête comme
// dans l'auteur d'une recette, qui sont deux pages différentes et une seule
// règle.
func nomAffichable(compte *core.Record) string {
	nom := strings.TrimSpace(compte.GetString("name"))
	if nom == "" {
		return compte.Email()
	}
	return nom
}
