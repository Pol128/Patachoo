package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
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

// --- Les recettes et leurs ingrédients : le sens du refus (PATA-64) --------

// L'appel de l'audit PATA-56, rejoué : Bob réduit à un point le titre de la
// recette d'Alice. La règle de suppression protégeait l'enregistrement, pas ce
// qu'il contient — il restait une recette vide, que son auteur seul pouvait
// effacer, et lui seul à ne rien pouvoir récupérer.
func TestLAPIRestNeModifiePasLaRecetteDunAutre(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	bob := compteDeTest(t, app, "bob@exemple.test")
	recette := recetteDeLAuteur(t, app, alice)

	rec := appelLAPI(t, mux, http.MethodPatch, "/recipes/records/"+recette.Id, bob, `{"title":"."}`)

	if rec.Code == http.StatusOK {
		t.Errorf("statut %d : le PATCH de Bob sur la recette d'Alice a abouti", rec.Code)
	}
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Tarte aux pommes" {
		t.Errorf("title = %q après le PATCH, attendu %q — statut %d, corps :\n%s",
			titre, "Tarte aux pommes", rec.Code, rec.Body.String())
	}
}

// Le sens de l'autorisation : une règle refermée sur personne satisferait le
// refus ci-dessus sans que rien ne le dise, et l'API serait cassée pour tout le
// monde.
func TestLAPIRestModifieToujoursSaPropreRecette(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	recette := recetteDeLAuteur(t, app, alice)

	rec := appelLAPI(t, mux, http.MethodPatch, "/recipes/records/"+recette.Id, alice,
		`{"title":"Tarte aux prunes"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Tarte aux prunes" {
		t.Errorf("title = %q après le PATCH, attendu %q", titre, "Tarte aux prunes")
	}
}

// Le chemin destructeur que l'audit décrit en toutes lettres : DELETE ligne par
// ligne jusqu'à ce qu'il n'en reste aucune — vérifié à 204 avant PATA-64.
// Fermer l'édition de la recette en laissant ingredients ouvert ne fermerait
// rien, puisque c'est par là que la recette se vide.
func TestLAPIRestNEcritPasDansLesIngredientsDeLaRecetteDunAutre(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	bob := compteDeTest(t, app, "bob@exemple.test")
	recette := recetteDeLAuteur(t, app, alice)
	creeIngredients(t, app, recette, []string{"3 pommes"})
	ligne := lesIngredientsDe(t, app, recette)[0]

	cas := []struct {
		nom      string
		methode  string
		chemin   string
		corps    string
		interdit int
	}{
		{"création", http.MethodPost, "/ingredients/records",
			`{"recipe":"` + recette.Id + `","raw":"200 g de farine"}`, http.StatusOK},
		{"modification", http.MethodPatch, "/ingredients/records/" + ligne.Id,
			`{"raw":"rien du tout"}`, http.StatusOK},
		{"suppression", http.MethodDelete, "/ingredients/records/" + ligne.Id,
			"", http.StatusNoContent},
	}

	for _, c := range cas {
		rec := appelLAPI(t, mux, c.methode, c.chemin, bob, c.corps)
		if rec.Code == c.interdit {
			t.Errorf("%s : statut %d, l'appel de Bob a abouti", c.nom, rec.Code)
		}
	}

	// L'état enregistré fait foi : une ligne ajoutée, changée ou partie se lit
	// en base, pas dans un code de retour.
	lignes := lesIngredientsDe(t, app, recette)
	if len(lignes) != 1 {
		t.Fatalf("%d ingrédients après les trois appels de Bob, 1 attendu", len(lignes))
	}
	if brut := lignes[0].GetString("raw"); brut != "3 pommes" {
		t.Errorf("raw = %q après les trois appels de Bob, attendu %q", brut, "3 pommes")
	}
}

// L'autre chemin, que la règle de collection ne ferme pas : recipe n'est pas
// figé, et PocketBase n'évalue UpdateRule qu'en allant chercher la ligne, donc
// sur son état d'avant modification. Bob écrit dans sa propre recette — son
// droit —, puis retourne le rattachement de la ligne vers la recette d'Alice :
// la règle a déjà dit oui, et la ligne atterrit sous un plat qu'il n'a pas
// cuisiné. Rien ne borne la répétition.
//
// C'est exactement ce que fige("author", "recipe") tient déjà pour les notes ;
// ingredients n'avait pas l'équivalent.
func TestLAPIRestNeDeplacePasUnIngredientVersLaRecetteDunAutre(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	bob := compteDeTest(t, app, "bob@exemple.test")
	deAlice := recetteDeLAuteur(t, app, alice)
	deBob := recetteDeLAuteur(t, app, bob)

	cree := appelLAPI(t, mux, http.MethodPost, "/ingredients/records", bob,
		`{"recipe":"`+deBob.Id+`","raw":"200 g de farine"}`)
	if cree.Code != http.StatusOK {
		t.Fatalf("création dans sa propre recette : statut %d, attendu %d — corps :\n%s",
			cree.Code, http.StatusOK, cree.Body.String())
	}
	ligne := lesIngredientsDe(t, app, deBob)[0]

	deplace := appelLAPI(t, mux, http.MethodPatch, "/ingredients/records/"+ligne.Id, bob,
		`{"recipe":"`+deAlice.Id+`"}`)

	// L'état enregistré fait foi, pas le code de retour : refuser l'appel ou
	// remettre le rattachement d'origine sont deux façons acceptables de ne pas
	// déplacer la ligne.
	if lignes := lesIngredientsDe(t, app, deAlice); len(lignes) != 0 {
		t.Errorf("%d ingrédients sous la recette d'Alice après le PATCH de Bob, 0 attendu — statut %d, corps :\n%s",
			len(lignes), deplace.Code, deplace.Body.String())
	}
}

// Le sens de l'autorisation, côté ingrédients : Alice vide toujours sa propre
// recette, et l'API rend toujours 204.
func TestLAPIRestSupprimeToujoursUnIngredientDeSaPropreRecette(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	recette := recetteDeLAuteur(t, app, alice)
	creeIngredients(t, app, recette, []string{"3 pommes"})
	ligne := lesIngredientsDe(t, app, recette)[0]

	rec := appelLAPI(t, mux, http.MethodDelete, "/ingredients/records/"+ligne.Id, alice, "")

	if rec.Code != http.StatusNoContent {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if lignes := lesIngredientsDe(t, app, recette); len(lignes) != 0 {
		t.Errorf("%d ingrédients après la suppression, 0 attendu", len(lignes))
	}
}

// Le carnet reste partagé en lecture : c'est ce que la décision du 16/09/2026
// laisse expressément ouvert, et une fermeture trop large l'emporterait sans
// qu'aucun test de refus ne rougisse.
func TestLAPIRestLaisseLireLaRecetteDunAutre(t *testing.T) {
	app, mux := serveurDeTest(t)
	alice := compteDeTest(t, app, "alice@exemple.test")
	bob := compteDeTest(t, app, "bob@exemple.test")
	recette := recetteDeLAuteur(t, app, alice)
	creeIngredients(t, app, recette, []string{"3 pommes"})

	lectures := map[string]string{
		"liste des recettes":    "/recipes/records",
		"vue d'une recette":     "/recipes/records/" + recette.Id,
		"liste des ingrédients": "/ingredients/records",
	}
	for nom, chemin := range lectures {
		rec := appelLAPI(t, mux, http.MethodGet, chemin, bob, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s : statut %d, attendu %d — corps :\n%s",
				nom, rec.Code, http.StatusOK, rec.Body.String())
		}
	}
}

// recetteDeLAuteur enregistre une recette signée du compte donné : c'est l'état
// de départ de tout ce qui se joue entre deux identités.
func recetteDeLAuteur(t *testing.T, app core.App, auteur *core.Record) *core.Record {
	t.Helper()

	recette := recetteVierge(t, app)
	recette.Set(champAuteur, auteur.Id)
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette : %v", err)
	}
	return recette
}

// lesIngredientsDe relit les lignes depuis la base : c'est ce qui est écrit qui
// compte, pas ce qu'un code de retour annonce.
func lesIngredientsDe(t *testing.T, app core.App, recette *core.Record) []*core.Record {
	t.Helper()

	lignes, err := app.FindRecordsByFilter("ingredients", "recipe = {:recette}", "created", 0, 0,
		dbx.Params{"recette": recette.Id})
	if err != nil {
		t.Fatalf("ingrédients de %s : %v", recette.Id, err)
	}
	return lignes
}

// modifieLaNoteParLAPI joue un PATCH sur la collection des notes.
func modifieLaNoteParLAPI(
	t *testing.T,
	mux http.Handler,
	note string,
	compte *core.Record,
	corps string,
) *httptest.ResponseRecorder {
	t.Helper()

	return appelLAPI(t, mux, http.MethodPatch, "/comments/records/"+note, compte, corps)
}

// appelLAPI joue une requête sur /api/collections, authentifiée par l'en-tête
// Authorization qu'un client d'API porte — le chemin que PocketBase ouvre à
// côté de nos pages, et que nos routes ne gardent pas. Seules les règles de
// collection s'y opposent.
func appelLAPI(
	t *testing.T,
	mux http.Handler,
	methode, chemin string,
	compte *core.Record,
	corps string,
) *httptest.ResponseRecorder {
	t.Helper()

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("jeton du compte %s : %v", compte.Id, err)
	}

	req := httptest.NewRequest(methode, "/api/collections"+chemin, strings.NewReader(corps))
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
