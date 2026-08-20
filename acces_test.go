package main

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// created_by est écrasé par l'identité de l'appelant, sans regarder ce que le
// client a envoyé. Un champ qu'on ne remplirait que s'il est vide resterait
// falsifiable : il suffirait de l'envoyer rempli.
func TestLaCreationAttribueLaRecetteALAppelant(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	appelant := compteDeTest(t, app, "appelant@exemple.test")
	autre := compteDeTest(t, app, "autre@exemple.test")

	recette := recetteVierge(t, app)
	recette.Set("created_by", autre.Id) // ce que le client a posté

	if err := declenche(t, app.OnRecordCreateRequest("recipes"), app, recette, appelant); err != nil {
		t.Fatalf("hook de création : %v", err)
	}

	if pose := recette.GetString("created_by"); pose != appelant.Id {
		t.Errorf("created_by = %q, attendu l'id de l'appelant %q", pose, appelant.Id)
	}
}

// Symétrique de la création. Sans elle, tout compte connecté — qui a le droit
// de modifier — s'attribue la recette d'un autre, puis la supprime : la règle
// de suppression ne protège plus rien.
func TestUneModificationNeChangePasLAuteur(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	auteur := compteDeTest(t, app, "auteur@exemple.test")
	autre := compteDeTest(t, app, "autre@exemple.test")

	recette := recetteNeuve(t, app)
	recette.Set("created_by", auteur.Id)
	if err := app.Save(recette); err != nil {
		t.Fatalf("attribution de la recette : %v", err)
	}

	// Relue depuis la base : c'est cette lecture que Original() rend au hook.
	modifiee, err := app.FindRecordById("recipes", recette.Id)
	if err != nil {
		t.Fatalf("relecture de la recette : %v", err)
	}
	modifiee.Set("created_by", autre.Id)

	if err := declenche(t, app.OnRecordUpdateRequest("recipes"), app, modifiee, autre); err != nil {
		t.Fatalf("hook de modification : %v", err)
	}

	if pose := modifiee.GetString("created_by"); pose != auteur.Id {
		t.Errorf("created_by = %q après la modification, attendu l'auteur d'origine %q", pose, auteur.Id)
	}
}

// Un superuser n'est pas un compte de users : lui coller son propre
// identifiant dans created_by produirait une relation vers un enregistrement
// qui n'existe pas, et l'administration ne pourrait plus créer de recette du
// tout. C'est aussi le seul endroit d'où une recette se réattribue à la main.
func TestLAdministrationGardeLAuteurQuElleIndique(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	auteur := compteDeTest(t, app, "auteur@exemple.test")
	administrateur := superuserDeTest(t, app, "admin@exemple.test")

	recette := recetteVierge(t, app)
	recette.Set("created_by", auteur.Id)

	if err := declenche(t, app.OnRecordCreateRequest("recipes"), app, recette, administrateur); err != nil {
		t.Fatalf("hook de création : %v", err)
	}

	if pose := recette.GetString("created_by"); pose != auteur.Id {
		t.Errorf("created_by = %q, attendu l'auteur indiqué par l'administration %q", pose, auteur.Id)
	}
}

// Les règles d'accès interdisent déjà la création sans compte ; si un chemin y
// menait quand même, la valeur postée ne doit pas survivre — et le hook ne
// doit pas paniquer sur un appelant absent.
func TestSansCompteAuthentifieLaRecetteResteSansAuteur(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	autre := compteDeTest(t, app, "autre@exemple.test")

	recette := recetteVierge(t, app)
	recette.Set("created_by", autre.Id)

	if err := declenche(t, app.OnRecordCreateRequest("recipes"), app, recette, nil); err != nil {
		t.Fatalf("hook de création : %v", err)
	}

	if pose := recette.GetString("created_by"); pose != "" {
		t.Errorf("created_by = %q, attendu vide", pose)
	}
}

// declenche joue un hook de requête comme l'API REST le fait : l'appelant
// authentifié dans l'événement, l'enregistrement dedans, et le traitement qui
// suit remplacé par un passe-plat.
func declenche(
	t *testing.T,
	crochet interface {
		Trigger(*core.RecordRequestEvent, ...func(*core.RecordRequestEvent) error) error
	},
	app core.App,
	enregistrement *core.Record,
	appelant *core.Record,
) error {
	t.Helper()

	e := &core.RecordRequestEvent{}
	e.RequestEvent = &core.RequestEvent{App: app}
	e.Auth = appelant
	e.Collection = enregistrement.Collection()
	e.Record = enregistrement

	return crochet.Trigger(e, func(*core.RecordRequestEvent) error { return nil })
}

// recetteVierge rend une recette non enregistrée : les hooks de requête
// travaillent avant l'écriture.
func recetteVierge(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", "Tarte aux pommes")
	return recette
}

func compteDeTest(t *testing.T, app core.App, courriel string) *core.Record {
	t.Helper()

	return compteDansCollection(t, app, "users", courriel)
}

func superuserDeTest(t *testing.T, app core.App, courriel string) *core.Record {
	t.Helper()

	return compteDansCollection(t, app, core.CollectionNameSuperusers, courriel)
}

func compteDansCollection(t *testing.T, app core.App, nom, courriel string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId(nom)
	if err != nil {
		t.Fatalf("collection %s : %v", nom, err)
	}
	compte := core.NewRecord(collection)
	compte.SetEmail(courriel)
	compte.SetPassword("mot-de-passe-de-test")
	if err := app.Save(compte); err != nil {
		t.Fatalf("enregistrement du compte %s : %v", courriel, err)
	}
	return compte
}
