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
		// Ni le courriel saisi ni le mot de passe ne sont renvoyés à la page :
		// le second n'a rien à faire dans du HTML, fût-il le sien.
		return rendre(e, "connexion.html", "connexion-corps.html", donneesPage{
			Titre:   "Connexion — Patachoo",
			Message: messageEchecConnexion,
		})
	}

	jeton, err := compte.NewAuthToken()
	if err != nil {
		return err
	}
	poseLeCookieDeSession(e, cookieDeSession(jeton, compte.Collection().AuthToken.DurationTime()))

	return e.Redirect(http.StatusSeeOther, "/")
}

// deconnexion efface le cookie et renvoie à l'accueil.
func deconnexion(e *core.RequestEvent) error {
	poseLeCookieDeSession(e, cookieDeSessionEfface())
	return e.Redirect(http.StatusSeeOther, "/")
}
