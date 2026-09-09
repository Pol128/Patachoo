package main

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// messageEchecConnexion est le seul message qu'un échec produise, quel qu'en
// soit le motif : un message par cas ferait de la page de connexion un
// annuaire des comptes existants.
const messageEchecConnexion = "Courriel ou mot de passe incorrect."

// messageDebitDepasse est ce que voit celui qui a dépassé le plafond.
//
// Il ne nomme ni compte ni courriel : le plafond se compte par adresse, donc
// dire quoi que ce soit du compte visé rouvrirait par un autre canal ce que
// messageEchecConnexion ferme.
const messageDebitDepasse = "Trop de tentatives de connexion. Réessayez dans une minute."

// prioriteRattrapageDuDebit place notre rattrapage juste au-dessus du limiteur
// global de PocketBase.
//
// Les middlewares du routeur et ceux de la route sont fondus dans un même
// crochet trié par priorité croissante (tools/router/router.go) : une priorité
// strictement inférieure enveloppe donc le limiteur, et notre e.Next() reçoit
// l'erreur qu'il remonte. Un cran, comme pour la session : plus l'écart est
// petit, moins il reste de place pour qu'un middleware tiers vienne s'y
// glisser.
const prioriteRattrapageDuDebit = apis.DefaultRateLimitMiddlewarePriority - 1

// rendLeDepassementEnHTML rattrape le refus du limiteur pour le rendre en page.
//
// PocketBase répond au dépassement par e.TooManyRequestsError(""), que
// router.ErrorHandler écrit en JSON — et cet écrivain-là n'est pas
// configurable. Or POST /connexion est un formulaire HTML ordinaire :
// l'utilisateur verrait du JSON brut à la place de sa page. ErrorHandler
// s'abstient si la réponse est déjà écrite, d'où ce rattrapage, qui rend la
// page avant lui.
//
// Sur cette seule route, et non sur le routeur : l'API REST parle JSON à ses
// clients, et lui rendre une page de connexion remplacerait une erreur lisible
// par du HTML qu'aucun client ne sait lire.
func rendLeDepassementEnHTML() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooDepassementConnexion",
		Priority: prioriteRattrapageDuDebit,
		Func: func(e *core.RequestEvent) error {
			err := e.Next()
			if err == nil || router.ToApiError(err).Status != http.StatusTooManyRequests {
				return err
			}

			return rendreAvecStatut(e, http.StatusTooManyRequests, "connexion.html", "connexion-corps.html", &donneesPage{
				Titre:   "Connexion — Patachoo",
				Message: messageDebitDepasse,
			})
		},
	}
}

// pageConnexion sert le formulaire.
//
// Un compte déjà connecté n'a rien à y faire : il est renvoyé à l'accueil.
func pageConnexion(e *core.RequestEvent) error {
	if e.Auth != nil {
		return e.Redirect(http.StatusSeeOther, "/")
	}
	return rendre(e, "connexion.html", "connexion-corps.html", &donneesPage{
		Titre: "Connexion — Patachoo",
	})
}

// connexion vérifie les identifiants, dépose le cookie et redirige.
//
// Une redirection, et non un fragment : une réponse HTMX ne rend pas la mise
// en page, et l'en-tête resterait donc sur son état de visiteur.
func connexion(e *core.RequestEvent) error {
	courriel := strings.TrimSpace(e.Request.FormValue("courriel"))
	motDePasse := e.Request.FormValue("mot-de-passe")

	compte, err := e.App.FindAuthRecordByEmail("users", courriel)
	if err != nil || !compte.ValidatePassword(motDePasse) {
		// Un courriel inconnu n'offre aucun mot de passe à vérifier, donc rien
		// à hacher : sans le contrôle factice, il sort d'ici en quelques
		// microsecondes quand un courriel connu paie le bcrypt complet.
		if err != nil {
			controleFacticeDuMotDePasse(e)
		}
		return echecDeConnexion(e)
	}

	// Les identifiants sont bons ; reste à savoir si la collection autorise
	// cette connexion-là. Même refus que ci-dessus : dire lequel des deux a
	// échoué renseignerait sur l'existence du compte.
	if !laConnexionEstAutorisee(e, compte) {
		return echecDeConnexion(e)
	}

	jeton, err := compte.NewAuthToken()
	if err != nil {
		return err
	}
	poseLeCookieDeSession(e, cookieDeSession(jeton, compte.Collection().AuthToken.DurationTime()))

	return e.Redirect(http.StatusSeeOther, "/")
}

// controleFacticeDuMotDePasse paie le coût d'un hachage alors qu'il n'y a rien
// à vérifier.
//
// C'est le seul moyen d'égaliser le temps de réponse des deux refus : le
// message est déjà identique, mais un écart d'un ou deux ordres de grandeur se
// mesure sur le réseau et fait de la page de connexion un annuaire des comptes
// existants par un autre canal. PocketBase pose la même parade au même endroit
// de sa propre route (apis/record_auth_with_password.go, dummyPasswordCheck) ;
// elle n'y est pas exportée, d'où cette reprise.
//
// Une collection introuvable ou vide ne laisse rien à hacher et le refus repart
// aussitôt : sans compte, il n'y a aucune existence à déduire du temps.
func controleFacticeDuMotDePasse(e *core.RequestEvent) {
	quelconque := &core.Record{}
	if err := e.App.RecordQuery("users").Limit(1).One(quelconque); err != nil {
		return
	}

	// Ni la valeur soumise ni le résultat n'importent : seul le temps passé
	// dans bcrypt est l'objet de l'appel.
	_ = quelconque.ValidatePassword("")
}

// laConnexionEstAutorisee applique les deux contrôles que PocketBase pose sur
// sa propre route d'authentification, et que celle-ci, écrite à la main,
// contournerait sans eux.
//
// PasswordAuth.Enabled est l'interrupteur de l'administration
// (apis/record_auth_with_password.go) : fermé, il doit couper cette page-ci
// aussi, et pas seulement l'API REST. Il ne vaut que pour l'ouverture : une
// session déjà tenue ne se coupe pas parce que l'un des moyens de l'ouvrir a
// été fermé.
//
// Le second contrôle est la règle d'authentification, et il ne s'arrête pas à
// la connexion — voir laRegleDAuthentificationAutorise.
func laConnexionEstAutorisee(e *core.RequestEvent, compte *core.Record) bool {
	if !compte.Collection().PasswordAuth.Enabled {
		return false
	}
	return laRegleDAuthentificationAutorise(e, compte)
}

// laRegleDAuthentificationAutorise dit si la collection accorde encore une
// session à ce compte.
//
// AuthRule dit quels comptes ont le droit d'en tenir une — non vérifié,
// suspendu, restreint par une règle de collection. PocketBase ne la lit jamais
// au chargement du jeton, mais à chacun des deux moments où il en émet un :
// l'authentification et le renouvellement (apis/record_helpers.go,
// recordAuthResponse, appelé par record_auth_with_password.go comme par
// record_auth_refresh.go). Les deux, et pas seulement le premier : un contrôle
// posé à la seule ouverture ne reverrait plus jamais un compte qui ne se
// reconnecte pas, et chaque renouvellement repousserait son échéance.
//
// Le doute vaut refus : une règle illisible ferme la porte plutôt que de
// l'ouvrir en silence.
func laRegleDAuthentificationAutorise(e *core.RequestEvent, compte *core.Record) bool {
	infos, err := e.RequestInfo()
	if err != nil {
		e.App.Logger().Error("informations de requête illisibles", "erreur", err)
		return false
	}

	autorise, err := e.App.CanAccessRecord(compte, infos, compte.Collection().AuthRule)
	if err != nil {
		e.App.Logger().Error("règle d'authentification inapplicable", "erreur", err)
		return false
	}
	return autorise
}

// echecDeConnexion réaffiche le formulaire avec le seul message qu'un échec
// produise.
//
// Ni le courriel saisi ni le mot de passe ne sont renvoyés à la page : le
// second n'a rien à faire dans du HTML, fût-il le sien.
func echecDeConnexion(e *core.RequestEvent) error {
	return rendre(e, "connexion.html", "connexion-corps.html", &donneesPage{
		Titre:   "Connexion — Patachoo",
		Message: messageEchecConnexion,
	})
}

// deconnexion efface le cookie et renvoie à l'accueil.
func deconnexion(e *core.RequestEvent) error {
	poseLeCookieDeSession(e, cookieDeSessionEfface())
	return e.Redirect(http.StatusSeeOther, "/")
}
