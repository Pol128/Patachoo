package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// PATA-128. La garde du droit, jouée sur une route témoin.
//
// Aucune page de l'établi n'existe encore — elles sont PATA-125 à PATA-127, et
// c'est là que `.Bind(exigeUnCurateur())` se posera, route par route. Le patron
// est celui de `sonde` (session_test.go) : `serveurDeTest` accepte des routes
// de test, et une route témoin dit ce que la garde fait sans attendre qu'un
// écran soit écrit.
func sondeCurateur(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/sonde-curateur", func(e *core.RequestEvent) error {
		return e.String(http.StatusOK, "ok")
	}).Bind(exigeUnCurateur())
}

// Le visiteur reçoit la redirection, pas le refus : un curateur dont la session
// a expiré doit pouvoir se reconnecter, pas se cogner à un mur. C'est aussi ce
// que le site fait déjà sur ses douze routes d'écriture, par exigeUneSession().
func TestUnVisiteurEstRenvoyeVersLaConnexion(t *testing.T) {
	_, mux := serveurDeTest(t, sondeCurateur)

	rec := avecCookie(mux, http.MethodGet, "/sonde-curateur", nil)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
		t.Errorf("Location = %q, attendu %q", lieu, "/connexion")
	}
}

// Le sens du refus (DOD.md §3) : ce qu'un compte connecté ne peut pas faire.
//
// Un 403 et non un 404, tranché par Paul le 20/09/2026 : l'établi oppose son
// refus à un compte déjà connu de l'instance, et « cette page existe, mais pas
// pour toi » est plus honnête et plus simple à déboguer le jour où le droit a
// été oublié sur un compte. L'écart avec pageInscription, qui rend un 404 sur
// une inscription fermée, est volontaire — les deux ne protègent pas la même
// chose.
func TestUnCompteSansLeDroitEstRefuse(t *testing.T) {
	app, mux := serveurDeTest(t, sondeCurateur)
	compte := compteDeTest(t, app, "sans-droit@exemple.test")

	rec := avecEnTete(mux, http.MethodGet, "/sonde-curateur", jetonDe(t, compte), nil)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	// Le corps reste sobre : il dit que l'accès est refusé, il ne nomme ni la
	// garde ni le champ. Un refus qui énonce le nom du droit apprend à qui le
	// lit par où insister.
	corps := strings.ToLower(rec.Body.String())
	for _, mot := range []string{"curator", "curateur"} {
		if strings.Contains(corps, mot) {
			t.Errorf("le corps du refus contient %q — il nomme la garde ou le champ :\n%s",
				mot, rec.Body.String())
		}
	}
}

// Le sens de l'autorisation : sans lui, une garde qui refuse tout le monde
// passerait tous les tests de refus.
func TestUnCompteAvecLeDroitPasse(t *testing.T) {
	app, mux := serveurDeTest(t, sondeCurateur)
	compte := curateurDeTest(t, app, "curateur@exemple.test")

	rec := avecEnTete(mux, http.MethodGet, "/sonde-curateur", jetonDe(t, compte), nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Le retrait prend effet tout de suite, et c'est ce qui commande où la garde
// lit le droit : `e.Auth` est l'enregistrement relu en base à chaque requête,
// le jeton ne porte pas le champ. Toute valeur recopiée en session — ou mise en
// cache — rendrait ce test faux, et laisserait un droit retiré vivre jusqu'à la
// prochaine reconnexion.
func TestLeDroitRetireEnBasePrendEffetSansReconnexion(t *testing.T) {
	app, mux := serveurDeTest(t, sondeCurateur)
	compte := compteParDefaut(t, app)
	faisCurateur(t, app, compte, true)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	if rec := avecCookie(mux, http.MethodGet, "/sonde-curateur", cookie); rec.Code != http.StatusOK {
		t.Fatalf("statut %d avant le retrait, attendu %d — corps :\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}

	faisCurateur(t, app, compte, false)

	rec := avecCookie(mux, http.MethodGet, "/sonde-curateur", cookie)
	if rec.Code != http.StatusForbidden {
		t.Errorf("statut %d avec le même cookie après le retrait, attendu %d — corps :\n%s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// --- Le trou d'un droit porté par la collection des comptes ----------------

// users.UpdateRule vaut `id = @request.auth.id` : un compte connecté modifie
// bel et bien son propre enregistrement par l'API REST. Sans un gel, il s'y
// cocherait le droit lui-même, et la garde ne vaudrait rien.
func TestLAPIRestNeLaissePasUnCompteSeFaireCurateur(t *testing.T) {
	app, mux := serveurDeTest(t)
	compte := compteDeTest(t, app, "ordinaire@exemple.test")

	appelLAPI(t, mux, http.MethodPatch, "/users/records/"+compte.Id, compte, `{"curator":true}`)

	// L'état enregistré fait foi, pas le code de retour : refuser l'appel ou
	// remettre la valeur d'origine sont deux façons acceptables de ne pas
	// accorder le droit.
	if relitLeCompte(t, app, compte.Id).GetBool(champCurateur) {
		t.Error("un compte ordinaire s'est attribué le droit par PATCH sur son propre enregistrement")
	}
}

// Le pendant, et il n'est pas décoratif : `fige` recopie la valeur enregistrée
// par `Original().GetString(champ)`, une lecture écrite pour des champs texte.
// Sur un booléen, elle passe par "true"/"false" — ce test est la preuve que la
// conversion tient. Sans lui, un gel qui rendrait toujours faux passerait le
// test précédent sans rien tenir, et dépouillerait au passage tout curateur qui
// touche à son profil.
func TestLAPIRestNeRetirePasLeDroitAUnCurateur(t *testing.T) {
	app, mux := serveurDeTest(t)
	compte := curateurDeTest(t, app, "curateur@exemple.test")

	appelLAPI(t, mux, http.MethodPatch, "/users/records/"+compte.Id, compte, `{"curator":false}`)

	if !relitLeCompte(t, app, compte.Id).GetBool(champCurateur) {
		t.Error("un curateur a perdu son droit en modifiant son propre enregistrement")
	}
}

// L'octroi passe par /_/, c'est-à-dire par l'API REST sous un jeton de
// superuser : aucun écran n'est écrit pour ça, et le premier porteur du droit
// s'y désigne en dix secondes. Un gel sans exception fermerait la seule porte
// par laquelle le droit s'accorde, et livrerait une garde que personne ne peut
// franchir.
func TestLeSuperuserAccordeLeDroitParLAPI(t *testing.T) {
	app, mux := serveurDeTest(t)
	compte := compteDeTest(t, app, "futur-curateur@exemple.test")
	administrateur := superuserDeTest(t, app, "admin@exemple.test")

	rec := appelLAPI(t, mux, http.MethodPatch, "/users/records/"+compte.Id,
		administrateur, `{"curator":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !relitLeCompte(t, app, compte.Id).GetBool(champCurateur) {
		t.Error("le superuser n'a pas pu accorder le droit : l'établi resterait vide de curateurs")
	}
}

// --- Fixtures ---------------------------------------------------------------

// curateurDeTest crée un compte qui porte le droit.
func curateurDeTest(t *testing.T, app core.App, courriel string) *core.Record {
	t.Helper()

	compte := compteDeTest(t, app, courriel)
	faisCurateur(t, app, compte, true)
	return compte
}

// faisCurateur pose ou retire le droit en base, comme le ferait une case cochée
// dans /_/.
func faisCurateur(t *testing.T, app core.App, compte *core.Record, droit bool) {
	t.Helper()

	compte.Set(champCurateur, droit)
	if err := app.Save(compte); err != nil {
		t.Fatalf("pose du droit sur %s : %v", compte.Id, err)
	}
}

// relitLeCompte relit l'enregistrement en base : c'est lui qui fait foi, pas
// l'objet que le test garde en main.
func relitLeCompte(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()

	compte, err := app.FindRecordById("users", id)
	if err != nil {
		t.Fatalf("relecture du compte %s : %v", id, err)
	}
	return compte
}

// jetonDe émet un jeton d'authentification pour le compte donné.
func jetonDe(t *testing.T, compte *core.Record) string {
	t.Helper()

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("jeton du compte %s : %v", compte.Id, err)
	}
	return jeton
}
