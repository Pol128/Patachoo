package main

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// messageEchecConnexion est le seul message qu'un échec produise, quel qu'en
// soit le motif : un message par cas ferait de la page de connexion un
// annuaire des comptes existants.
const messageEchecConnexion = "Courriel ou mot de passe incorrect."

// pageConnexion sert le formulaire.
//
// Un compte déjà connecté n'a rien à y faire : il est renvoyé à l'accueil.
func pageConnexion(e *core.RequestEvent) error {
	if e.Auth != nil {
		return e.Redirect(http.StatusSeeOther, "/")
	}
	return rendre(e, "connexion.html", "connexion-corps.html", donneesPage{
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

// laConnexionEstAutorisee applique les deux contrôles que PocketBase pose sur
// sa propre route d'authentification, et que celle-ci, écrite à la main,
// contournerait sans eux.
//
// PasswordAuth.Enabled est l'interrupteur de l'administration
// (apis/record_auth_with_password.go) : fermé, il doit couper cette page-ci
// aussi, et pas seulement l'API REST.
//
// AuthRule dit quels comptes ont le droit d'ouvrir une session — non vérifié,
// suspendu, restreint par une règle de collection. PocketBase ne la lit qu'ici,
// à l'authentification (apis/record_helpers.go, recordAuthResponse), et jamais
// au chargement du jeton : un jeton émis en la violant resterait valable
// jusqu'à son échéance, cinq jours durant.
//
// Le doute vaut refus : une règle illisible ferme la porte plutôt que de
// l'ouvrir en silence.
func laConnexionEstAutorisee(e *core.RequestEvent, compte *core.Record) bool {
	collection := compte.Collection()
	if !collection.PasswordAuth.Enabled {
		return false
	}

	infos, err := e.RequestInfo()
	if err != nil {
		e.App.Logger().Error("informations de requête illisibles", "erreur", err)
		return false
	}

	autorise, err := e.App.CanAccessRecord(compte, infos, collection.AuthRule)
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
	return rendre(e, "connexion.html", "connexion-corps.html", donneesPage{
		Titre:   "Connexion — Patachoo",
		Message: messageEchecConnexion,
	})
}

// deconnexion efface le cookie et renvoie à l'accueil.
func deconnexion(e *core.RequestEvent) error {
	poseLeCookieDeSession(e, cookieDeSessionEfface())
	return e.Redirect(http.StatusSeeOther, "/")
}
