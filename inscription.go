package main

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// Les deux seuls messages qu'un refus produise.
//
// Le premier est volontairement muet sur son motif : dire qu'un courriel est
// déjà pris ferait de la page d'inscription un annuaire des comptes existants,
// exactement ce que messageEchecConnexion évite sur la page d'à côté. Le
// second n'apprend rien à personne — celui qui a saisi les deux mots de passe
// sait déjà qu'ils diffèrent — et lui répondre « inscription impossible »
// serait une énigme.
const (
	messageEchecInscription       = "Inscription impossible avec ces informations."
	messageConfirmationDifferente = "Les deux mots de passe ne correspondent pas."
)

// pageInscription sert le formulaire, quand le réglage l'autorise.
//
// Fermée, la page n'existe pas : un 404, et non un refus poli qui dirait qu'il
// y a quelque chose derrière.
func pageInscription(e *core.RequestEvent) error {
	if !inscriptionOuverte(e.App) {
		return apis.NewNotFoundError("", nil)
	}
	return rendre(e, "inscription.html", "inscription-corps.html", &donneesPage{
		Titre: "Créer un compte — Patachoo",
	})
}

// inscription crée le compte, puis dépose le cookie de session.
//
// L'inscription connecte : renvoyer l'inscrit vers un second formulaire lui
// ferait ressaisir ce qu'il vient d'écrire.
func inscription(e *core.RequestEvent) error {
	// Relu ici et pas seulement sur le GET : rien n'oblige un client à
	// demander la page avant de poster, et une porte gardée d'un seul côté
	// n'est pas gardée.
	if !inscriptionOuverte(e.App) {
		return apis.NewNotFoundError("", nil)
	}

	// La liste blanche, et elle seule. Recopier le formulaire en vrac dans
	// l'enregistrement laisserait un inscrit se poser verified à vrai, ou
	// écrire tout champ ajouté plus tard à users sans que personne y repense.
	courriel := strings.TrimSpace(e.Request.FormValue("email"))
	motDePasse := e.Request.FormValue("password")
	confirmation := e.Request.FormValue("passwordConfirm")
	nom := strings.TrimSpace(e.Request.FormValue("name"))

	if motDePasse != confirmation {
		return echecDInscription(e, messageConfirmationDifferente)
	}

	collection, err := e.App.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}

	compte := core.NewRecord(collection)
	compte.SetEmail(courriel)
	compte.SetPassword(motDePasse)
	compte.Set("name", nom)

	if err := e.App.Save(compte); err != nil {
		// Le motif reste au journal : courriel déjà pris, mot de passe trop
		// court, courriel malformé. La page, elle, n'en dit rien. Le message
		// d'erreur est journalisé sans le formulaire, qui porte un mot de
		// passe.
		e.App.Logger().Info("inscription refusée", "erreur", err)
		return echecDInscription(e, messageEchecInscription)
	}

	jeton, err := compte.NewAuthToken()
	if err != nil {
		return err
	}
	poseLeCookieDeSession(e, cookieDeSession(jeton, collection.AuthToken.DurationTime()))

	return e.Redirect(http.StatusSeeOther, "/")
}

// echecDInscription réaffiche le formulaire avec le message donné.
func echecDInscription(e *core.RequestEvent, message string) error {
	return rendre(e, "inscription.html", "inscription-corps.html", &donneesPage{
		Titre:   "Créer un compte — Patachoo",
		Message: message,
	})
}

// inscriptionOuverte lit le réglage en base, à chaque appel.
//
// À chaque appel, et non une fois au démarrage : c'est ce qui permet à
// l'hébergeant de basculer l'inscription depuis /_/ sans redémarrer. Une
// lecture par requête sur une page qui n'est demandée qu'à l'inscription ne
// coûte rien.
//
// Le doute vaut refus, comme partout ailleurs : une collection illisible ou un
// enregistrement absent — base à moitié migrée, réglage effacé à la main —
// ferme la porte plutôt que de l'ouvrir en silence.
func inscriptionOuverte(app core.App) bool {
	reglages, err := app.FindAllRecords("settings")
	if err != nil {
		app.Logger().Error("réglages illisibles : inscription tenue fermée", "erreur", err)
		return false
	}
	if len(reglages) == 0 {
		return false
	}
	return reglages[0].GetBool("open_registration")
}
