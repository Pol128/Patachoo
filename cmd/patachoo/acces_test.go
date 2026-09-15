package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// --- Les notes : la signature et le rattachement ---------------------------

// L'appel de l'audit PATA-56, rejoué par le mux : Alice modifie sa propre note
// en y glissant l'identifiant de Bob, et la note se retrouve signée d'un compte
// qui n'a rien écrit.
//
// UpdateRule ne l'arrête pas, et ce n'est pas un oubli de la règle : PocketBase
// ne l'évalue qu'en allant chercher la ligne, donc sur son état d'avant, où la
// note est bien celle d'Alice. Le changement d'author vient après. Le hook est
// tout ce qui reste entre le texte d'Alice et la signature de Bob.
//
// Par le mux, et non par le hook à la main : c'est la chaîne entière qui est en
// cause — la règle qui laisse passer, puis le hook qui rattrape.
func TestLAPIRestNeSignePasUneNoteDuNomDUnAutreCompte(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	bob := compteDeTest(t, app, "bob@exemple.test")
	note := noteEnBase(t, app, recetteNeuve(t, app), alice, "Trop cuit de dix minutes.")

	const texte = "Je rate tout ce que je cuisine."
	rec := modifieLaNoteParLAPI(t, mux, note.Id, alice,
		`{"body":"`+texte+`","author":"`+bob.Id+`"}`)

	// L'état enregistré fait foi, pas le code de retour : la tâche ne l'impose
	// pas, et un refus comme un succès sont deux façons acceptables de ne pas
	// retourner la note.
	relue := relitLaNote(t, app, note.Id)
	if auteur := relue.GetString("author"); auteur != alice.Id {
		t.Errorf("author = %q après le PATCH, attendu l'id d'Alice %q — statut %d, corps :\n%s",
			auteur, alice.Id, rec.Code, rec.Body.String())
	}
	if corps := relue.GetString("body"); corps != texte {
		t.Errorf("body = %q après le PATCH, attendu %q — statut %d, corps :\n%s",
			corps, texte, rec.Code, rec.Body.String())
	}
}

// Le même appel déplace aussi une note sous une autre recette : recipe n'est
// pas davantage figé qu'author, et une note déplacée commente un plat que son
// auteur n'a pas cuisiné.
//
// En miroir de TestUneModificationNeChangePasLAuteur, et au même niveau : le
// hook seul, déclenché à la main. Le chemin complet est couvert par le test
// ci-dessus, le rejouer ici ne dirait rien de plus.
func TestUneModificationNeDeplacePasUneNote(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	auteur := compteDeTest(t, app, "auteur@exemple.test")
	recette := recetteNeuve(t, app)
	ailleurs := recetteNeuve(t, app)
	note := noteEnBase(t, app, recette, auteur, "Trop cuit.")

	// Relue depuis la base : c'est cette lecture que Original() rend au hook.
	deplacee := relitLaNote(t, app, note.Id)
	deplacee.Set("recipe", ailleurs.Id)

	if err := declenche(t, app.OnRecordUpdateRequest("comments"), app, deplacee, auteur); err != nil {
		t.Fatalf("hook de modification : %v", err)
	}

	if pose := deplacee.GetString("recipe"); pose != recette.Id {
		t.Errorf("recipe = %q après la modification, attendu la recette d'origine %q", pose, recette.Id)
	}
}

// modifieLaNoteParLAPI joue un PATCH sur la collection, authentifié par
// l'en-tête Authorization qu'un client d'API porte — le chemin que PocketBase
// ouvre à côté de nos pages, et que nos routes ne gardent pas.
func modifieLaNoteParLAPI(
	t *testing.T,
	mux http.Handler,
	note string,
	compte *core.Record,
	corps string,
) *httptest.ResponseRecorder {
	t.Helper()

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("jeton du compte %s : %v", compte.Id, err)
	}

	req := httptest.NewRequest(
		http.MethodPatch, "/api/collections/comments/records/"+note, strings.NewReader(corps))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", jeton)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
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
