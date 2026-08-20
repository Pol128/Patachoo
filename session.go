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

// Les deux priorités qui font tout tenir. Les handlers sont triés par priorité
// croissante (tools/hook/hook.go), donc :
//
//   - la recopie du cookie passe avant pbLoadAuthToken, qui ne lit le jeton que
//     dans l'en-tête Authorization (apis/middlewares.go, getAuthTokenFromRequest)
//     et qu'un navigateur demandant une page HTML n'envoie jamais ;
//   - le renouvellement passe après, puisqu'il lui faut e.Auth peuplé, et avant
//     que le gestionnaire n'écrive la réponse : un Set-Cookie posé après le
//     corps ne partirait pas.
const (
	prioriteRecopieDuCookie = apis.DefaultLoadAuthTokenMiddlewarePriority - 10
	prioriteRenouvellement  = apis.DefaultLoadAuthTokenMiddlewarePriority + 10
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
				e.SetCookie(cookieDeSession(jeton, duree))
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

	// Seule une session portée par notre cookie se renouvelle : un client qui
	// gère lui-même son jeton dans l'en-tête n'a que faire d'un Set-Cookie.
	cookie, err := e.Request.Cookie(nomCookieSession)
	if err != nil || cookie.Value == "" {
		return "", 0, false
	}

	duree := e.Auth.Collection().AuthToken.DurationTime()
	if duree <= 0 || !aPasseLaMiVie(cookie.Value, duree) {
		return "", 0, false
	}

	jeton, err := e.Auth.NewAuthToken()
	if err != nil {
		e.App.Logger().Error("renouvellement de la session impossible", "erreur", err)
		return "", 0, false
	}
	return jeton, duree, true
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
type utilisateur struct {
	Id  string
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
	return &utilisateur{Id: e.Auth.Id, Nom: nom}
}
