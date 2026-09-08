package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// --- Le montage ------------------------------------------------------------

// serveurConnecte monte le serveur, crée un compte et ouvre sa session : c'est
// l'état de départ des quatre routes, qui ne se servent qu'à un compte
// connecté.
func serveurConnecte(t *testing.T) (core.App, http.Handler, *http.Cookie) {
	t.Helper()

	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	return app, mux, cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
}

// fichierPoste porte un téléversement : le nom que le navigateur annonce et
// les octets envoyés. Le type MIME n'y figure pas — c'est le contenu qui le
// décide, comme chez PocketBase.
type fichierPoste struct {
	nom     string
	contenu []byte
}

// poste envoie le formulaire en multipart, qui est ce qu'envoie un formulaire
// portant un <input type="file"> : tester en urlencodé exercerait un décodage
// que le navigateur n'emprunte jamais.
func poste(t *testing.T, mux http.Handler, cible string, cookie *http.Cookie, champs url.Values, fichiers ...fichierPoste) *httptest.ResponseRecorder {
	t.Helper()

	corps := &bytes.Buffer{}
	ecrivain := multipart.NewWriter(corps)
	for nom, valeurs := range champs {
		for _, valeur := range valeurs {
			if err := ecrivain.WriteField(nom, valeur); err != nil {
				t.Fatalf("champ %q : %v", nom, err)
			}
		}
	}
	for _, fichier := range fichiers {
		partie, err := ecrivain.CreateFormFile("image", fichier.nom)
		if err != nil {
			t.Fatalf("fichier %q : %v", fichier.nom, err)
		}
		if _, err := partie.Write(fichier.contenu); err != nil {
			t.Fatalf("écriture de %q : %v", fichier.nom, err)
		}
	}
	if err := ecrivain.Close(); err != nil {
		t.Fatalf("clôture du corps multipart : %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, cible, corps)
	req.Header.Set("Content-Type", ecrivain.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// champsValides rend une saisie complète et correcte : les tests qui portent
// sur un seul champ partent de là et n'en changent qu'un.
func champsValides() url.Values {
	return url.Values{
		"titre":             {"Tarte aux pommes"},
		"portions":          {"6"},
		"temps-preparation": {"20"},
		"temps-cuisson":     {"40"},
		"instructions":      {"Éplucher, puis enfourner."},
		"ingredients":       {"500 g de farine\n3 pommes"},
		"tags":              {"dessert, pommes"},
		"saisons":           {"automne", "hiver"},
	}
}

// pngDeTest rend une image PNG réelle : le type MIME se lit dans les octets,
// pas dans le nom du fichier, donc un contenu bidon serait refusé pour la
// mauvaise raison.
func pngDeTest(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 200, G: 120, B: 40, A: 255})

	corps := &bytes.Buffer{}
	if err := png.Encode(corps, img); err != nil {
		t.Fatalf("encodage du PNG de test : %v", err)
	}
	return corps.Bytes()
}

// recettes rend toutes les recettes enregistrées.
func recettes(t *testing.T, app core.App) []*core.Record {
	t.Helper()

	trouvees, err := app.FindAllRecords("recipes")
	if err != nil {
		t.Fatalf("lecture des recettes : %v", err)
	}
	return trouvees
}

// laRecette rend l'unique recette enregistrée, ou fait échouer le test.
func laRecette(t *testing.T, app core.App) *core.Record {
	t.Helper()

	trouvees := recettes(t, app)
	if len(trouvees) != 1 {
		t.Fatalf("%d recettes enregistrées, une seule attendue", len(trouvees))
	}
	return trouvees[0]
}

// relitLaRecette relit la recette depuis la base : une édition se juge sur ce
// qui est écrit, pas sur l'enregistrement que le test tient déjà.
func relitLaRecette(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()

	recette, err := app.FindRecordById("recipes", id)
	if err != nil {
		t.Fatalf("relecture de la recette %s : %v", id, err)
	}
	return recette
}

// lignesDe rend les raw des ingrédients de la recette, dans l'ordre des
// position.
func lignesDe(t *testing.T, app core.App, recette *core.Record) []string {
	t.Helper()

	brutes := []string{}
	for _, ligne := range ingredientsDe(t, app, recette) {
		brutes = append(brutes, ligne.GetString("raw"))
	}
	return brutes
}

// ingredientsDe rend les ingrédients de la recette, triés par position.
func ingredientsDe(t *testing.T, app core.App, recette *core.Record) []*core.Record {
	t.Helper()

	lignes, err := app.FindAllRecords("ingredients", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		t.Fatalf("lecture des ingrédients : %v", err)
	}
	sort.SliceStable(lignes, func(i, j int) bool {
		return lignes[i].GetInt("position") < lignes[j].GetInt("position")
	})
	return lignes
}

// recetteEnregistree pose une recette directement en base, sans passer par le
// formulaire : c'est l'état de départ des tests d'édition.
func recetteEnregistree(t *testing.T, app core.App, champs map[string]any) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", "Tarte aux pommes")
	for nom, valeur := range champs {
		recette.Set(nom, valeur)
	}
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette : %v", err)
	}
	return recette
}

// poseLesIngredients rattache les lignes à la recette, dans l'ordre donné.
func poseLesIngredients(t *testing.T, app core.App, recette *core.Record, brutes ...string) {
	t.Helper()

	for i, brut := range brutes {
		ligne := ingredientNeuf(t, app, recette, brut)
		ligne.Set("position", i+1)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}
}

// --- Session : le sens du refus -------------------------------------------

// Les quatre routes sont des routes d'écriture ou d'accès à des recettes :
// sans session, elles ne rendent rien et renvoient à la page de connexion.
// C'est le critère qui passe avant tous les autres — il s'évalue avant même
// la recherche de la recette en édition.
func TestLesQuatreRoutesRefusentUnVisiteur(t *testing.T) {
	app, mux, _ := serveurConnecte(t)
	recette := recetteEnregistree(t, app, nil)

	cas := []struct {
		nom     string
		methode string
		cible   string
	}{
		{"formulaire de création", http.MethodGet, "/recettes/nouvelle"},
		{"création", http.MethodPost, "/recettes"},
		{"formulaire d'édition", http.MethodGet, "/recettes/" + recette.Id + "/modifier"},
		{"édition", http.MethodPost, "/recettes/" + recette.Id},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if c.methode == http.MethodGet {
				rec = avecCookie(mux, c.methode, c.cible, nil)
			} else {
				rec = poste(t, mux, c.cible, nil, champsValides())
			}

			if rec.Code != http.StatusSeeOther {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
			}
			if destination := rec.Header().Get("Location"); destination != "/connexion" {
				t.Errorf("Location %q, attendu %q", destination, "/connexion")
			}
			if corps := rec.Body.String(); strings.Contains(corps, "<form") {
				t.Errorf("un formulaire a été rendu à un visiteur :\n%s", corps)
			}
		})
	}
}

// Un refus qui répondrait bien mais écrirait quand même ne protégerait rien :
// les deux POST se vérifient sur la base, pas sur la réponse.
func TestUnPostSansSessionNecritRien(t *testing.T) {
	app, mux, _ := serveurConnecte(t)
	recette := recetteEnregistree(t, app, map[string]any{"title": "Titre d'origine"})
	poseLesIngredients(t, app, recette, "500 g de farine")

	champs := champsValides()
	champs.Set("titre", "Titre injecté")

	poste(t, mux, "/recettes", nil, champs)
	if n := len(recettes(t, app)); n != 1 {
		t.Errorf("%d recettes après une création sans session, 1 attendue", n)
	}

	poste(t, mux, "/recettes/"+recette.Id, nil, champs)
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Titre d'origine" {
		t.Errorf("titre %q après une édition sans session, %q attendu", titre, "Titre d'origine")
	}
	if lignes := lignesDe(t, app, recette); len(lignes) != 1 || lignes[0] != "500 g de farine" {
		t.Errorf("ingrédients %q après une édition sans session, inchangés attendus", lignes)
	}
}

// Le sens de l'autorisation, sans quoi le critère précédent serait satisfait
// par quatre routes cassées.
func TestLesQuatreRoutesServentUnCompteConnecte(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnregistree(t, app, nil)

	if rec := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie); rec.Code != http.StatusOK {
		t.Errorf("GET /recettes/nouvelle : statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if rec := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie); rec.Code != http.StatusOK {
		t.Errorf("GET /recettes/{id}/modifier : statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	rec := poste(t, mux, "/recettes", cookie, champsValides())
	if rec.Code != http.StatusSeeOther {
		t.Errorf("POST /recettes : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if n := len(recettes(t, app)); n != 2 {
		t.Errorf("%d recettes après la création, 2 attendues", n)
	}

	rec = poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides())
	if rec.Code != http.StatusSeeOther {
		t.Errorf("POST /recettes/{id} : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Tarte aux pommes" {
		t.Errorf("titre %q après l'édition, %q attendu", titre, "Tarte aux pommes")
	}
}

// --- Le formulaire ---------------------------------------------------------

func TestLeFormulaireNeufPorteLesDixChamps(t *testing.T) {
	_, mux, cookie := serveurConnecte(t)

	rec := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	champs := []string{
		"titre", "image", "portions", "temps-preparation", "temps-cuisson",
		"instructions", "ingredients", "tags", "type-de-plat", "saisons",
	}
	for _, champ := range champs {
		if !strings.Contains(corps, `name="`+champ+`"`) {
			t.Errorf("formulaire sans le champ %q :\n%s", champ, corps)
		}
	}
	if !strings.Contains(corps, `action="/recettes"`) {
		t.Errorf("formulaire sans action de création :\n%s", corps)
	}
	// Sans enctype, un navigateur poste le seul nom du fichier : le champ
	// image serait rendu et pourtant inopérant.
	if !strings.Contains(corps, `enctype="multipart/form-data"`) {
		t.Errorf("formulaire sans enctype multipart :\n%s", corps)
	}
}

// Le type de plat vient de meal_types, ordonné par position : un tri
// alphabétique mettrait « Dessert » avant « Entrée ».
func TestLeFormulaireOffreLesTypesDePlatDansLOrdre(t *testing.T) {
	_, mux, cookie := serveurConnecte(t)

	corps := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie).Body.String()

	precedent := -1
	for _, nom := range []string{"Entrée", "Plat", "Dessert", "Apéritif"} {
		position := strings.Index(corps, nom)
		if position < 0 {
			t.Fatalf("type de plat %q absent du formulaire :\n%s", nom, corps)
		}
		if position < precedent {
			t.Errorf("type de plat %q rendu hors de l'ordre des position :\n%s", nom, corps)
		}
		precedent = position
	}
}

func TestLeFormulaireOffreLesQuatreSaisons(t *testing.T) {
	_, mux, cookie := serveurConnecte(t)

	corps := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie).Body.String()

	for _, saison := range []string{"printemps", "été", "automne", "hiver"} {
		if !strings.Contains(corps, `value="`+saison+`"`) {
			t.Errorf("saison %q absente du formulaire :\n%s", saison, corps)
		}
	}
}

// --- La création -----------------------------------------------------------

func TestUnPostValideCreeLaRecetteEtSesIngredients(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	champs := champsValides()
	champs.Set("ingredients", "  500 g de farine  \n\n   \n3 pommes\nune pincée de sel")

	rec := poste(t, mux, "/recettes", cookie, champs)

	recette := laRecette(t, app)
	if destination := rec.Header().Get("Location"); destination != "/recettes/"+recette.Id {
		t.Errorf("Location %q, attendu %q", destination, "/recettes/"+recette.Id)
	}
	if titre := recette.GetString("title"); titre != "Tarte aux pommes" {
		t.Errorf("titre %q, attendu %q", titre, "Tarte aux pommes")
	}
	if portions := recette.GetInt("servings"); portions != 6 {
		t.Errorf("portions %d, attendues 6", portions)
	}
	if preparation := recette.GetInt("prep_time"); preparation != 20 {
		t.Errorf("temps de préparation %d, attendu 20", preparation)
	}
	if cuisson := recette.GetInt("cook_time"); cuisson != 40 {
		t.Errorf("temps de cuisson %d, attendu 40", cuisson)
	}
	if saisons := recette.GetStringSlice("seasons"); strings.Join(saisons, ",") != "automne,hiver" {
		t.Errorf("saisons %q, attendues [automne hiver]", saisons)
	}
	if tags := recette.GetStringSlice("tags"); len(tags) != 2 {
		t.Errorf("%d tags, 2 attendus", len(tags))
	}

	// Les lignes vides ou blanches ne font pas d'ingrédient, et les espaces
	// de bout sont rognés : raw est la ligne saisie, pas la ligne tapée.
	attendues := []string{"500 g de farine", "3 pommes", "une pincée de sel"}
	lignes := ingredientsDe(t, app, recette)
	if len(lignes) != len(attendues) {
		t.Fatalf("%d ingrédients, %d attendus : %q", len(lignes), len(attendues), lignesDe(t, app, recette))
	}
	for i, ligne := range lignes {
		if brut := ligne.GetString("raw"); brut != attendues[i] {
			t.Errorf("raw %q en %d, attendu %q", brut, i, attendues[i])
		}
		// Une position par ligne enregistrée, sans trou : une ligne blanche
		// sautée ne doit pas laisser de creux dans la numérotation.
		if position := ligne.GetInt("position"); position != i+1 {
			t.Errorf("position %d pour %q, attendue %d", position, ligne.GetString("raw"), i+1)
		}
	}
}

// created_by est posé depuis la session, jamais depuis le formulaire.
func TestUnePostValideAttribueLaRecetteAuCompteConnecte(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("compte de test : %v", err)
	}

	poste(t, mux, "/recettes", cookie, champsValides())

	if auteur := laRecette(t, app).GetString(champAuteur); auteur != compte.Id {
		t.Errorf("created_by %q, attendu %q", auteur, compte.Id)
	}
}

func TestUnPostSansTitreNeCreeRien(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	champs := champsValides()
	champs.Set("titre", "   ")

	rec := poste(t, mux, "/recettes", cookie, champs)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if n := len(recettes(t, app)); n != 0 {
		t.Errorf("%d recettes créées malgré le titre manquant, 0 attendue", n)
	}
	if n := nombreDIngredients(t, app); n != 0 {
		t.Errorf("%d ingrédients créés malgré le titre manquant, 0 attendu", n)
	}
	if !strings.Contains(strings.ToLower(corps), "titre") {
		t.Errorf("message d'erreur sans le nom du champ fautif :\n%s", corps)
	}
	// L'utilisateur ne retape rien : ce qu'il avait saisi lui revient.
	for _, saisi := range []string{"500 g de farine", "3 pommes", "Éplucher, puis enfourner."} {
		if !strings.Contains(corps, saisi) {
			t.Errorf("le formulaire re-rendu a perdu %q :\n%s", saisi, corps)
		}
	}
}

// --- L'édition -------------------------------------------------------------

func TestLeFormulaireDEditionEstPreRempli(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnregistree(t, app, map[string]any{
		"title":        "Clafoutis",
		"servings":     4,
		"prep_time":    15,
		"cook_time":    35,
		"instructions": "Mélanger.",
		"seasons":      []string{"été"},
	})
	poseLesIngredients(t, app, recette, "500 g de cerises", "3 œufs", "80 g de sucre")

	rec := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(corps, `action="/recettes/`+recette.Id+`"`) {
		t.Errorf("formulaire sans action d'édition :\n%s", corps)
	}
	for _, valeur := range []string{`value="Clafoutis"`, `value="4"`, `value="15"`, `value="35"`, "Mélanger."} {
		if !strings.Contains(corps, valeur) {
			t.Errorf("formulaire sans %q :\n%s", valeur, corps)
		}
	}
	// Les ingrédients reviennent un par ligne, dans l'ordre des position, à
	// partir de raw : c'est le texte que l'utilisateur avait saisi.
	if !strings.Contains(corps, "500 g de cerises\n3 œufs\n80 g de sucre") {
		t.Errorf("ingrédients non restitués un par ligne dans l'ordre :\n%s", corps)
	}
}

func TestUneEditionRemplaceLesIngredientsEnBloc(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnregistree(t, app, nil)
	poseLesIngredients(t, app, recette, "500 g de cerises", "3 œufs", "80 g de sucre")

	champs := champsValides()
	champs.Set("ingredients", "400 g de framboises\n3 œufs")
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	lignes := lignesDe(t, app, recette)
	if strings.Join(lignes, "|") != "400 g de framboises|3 œufs" {
		t.Errorf("ingrédients %q, attendus [400 g de framboises 3 œufs]", lignes)
	}
	// Une ligne restée de l'état précédent est un doublon invisible : les
	// ingrédients de la base entière comptent, pas seulement ceux de la
	// recette.
	if n := nombreDIngredients(t, app); n != 2 {
		t.Errorf("%d ingrédients en base, 2 attendus : des lignes de l'état précédent ont survécu", n)
	}
}

// Corriger une recette importée ne doit pas lui faire perdre son origine ni
// son auteur : trois champs qu'aucun champ du formulaire ne porte.
func TestUneEditionConserveLOrigineEtLAuteur(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	recette := recetteEnregistree(t, app, map[string]any{
		"source_url":  "https://exemple.fr/tarte",
		"source_name": "Exemple",
		champAuteur:   autre.Id,
	})

	poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides())

	relue := relitLaRecette(t, app, recette.Id)
	if source := relue.GetString("source_url"); source != "https://exemple.fr/tarte" {
		t.Errorf("source_url %q, attendue %q", source, "https://exemple.fr/tarte")
	}
	if nom := relue.GetString("source_name"); nom != "Exemple" {
		t.Errorf("source_name %q, attendu %q", nom, "Exemple")
	}
	if auteur := relue.GetString(champAuteur); auteur != autre.Id {
		t.Errorf("created_by %q, attendu %q", auteur, autre.Id)
	}
}

// Un champ created_by posté est falsifiable par nature : il n'est jamais lu,
// et le formulaire ne le porte pas.
func TestCreatedByNestJamaisLuDeLaRequete(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	recette := recetteEnregistree(t, app, map[string]any{champAuteur: autre.Id})

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()
	if strings.Contains(corps, champAuteur) {
		t.Errorf("le formulaire porte un champ %q :\n%s", champAuteur, corps)
	}

	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("compte de test : %v", err)
	}
	champs := champsValides()
	champs.Set(champAuteur, compte.Id)
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	if auteur := relitLaRecette(t, app, recette.Id).GetString(champAuteur); auteur != autre.Id {
		t.Errorf("created_by %q après un POST qui l'a proposé, %q attendu", auteur, autre.Id)
	}
}

// --- L'image ---------------------------------------------------------------

func TestUneEditionSansFichierConserveLImage(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	image := recette.GetString("image")
	if image == "" {
		t.Fatalf("aucune image enregistrée à la création")
	}

	poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides())

	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != image {
		t.Errorf("image %q après une édition sans fichier, %q attendue", apres, image)
	}
}

func TestUneCaseRetirerLImageLEfface(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	if recette.GetString("image") == "" {
		t.Fatalf("aucune image enregistrée à la création")
	}

	champs := champsValides()
	champs.Set("retirer-image", "1")
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != "" {
		t.Errorf("image %q après « retirer l'image », vide attendue", apres)
	}
}

// Un navigateur envoie la partie « image » même quand aucun fichier n'a été
// choisi : elle porte un nom vide et zéro octet.
//
// Le code ne s'en défend pas lui-même — il s'appuie sur mime/multipart, qui ne
// range dans les fichiers que les parties portant un nom (ReadForm). C'est
// exactement pour ça que ce test existe : l'hypothèse est invisible dans le
// code, et sa chute ferait échouer l'édition la plus ordinaire, celle où l'on
// ne touche pas à l'image.
func TestUneEditionAvecUnePartieDImageVideConserveLImage(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	image := recette.GetString("image")
	if image == "" {
		t.Fatalf("aucune image enregistrée à la création")
	}

	rec := poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides(), fichierPoste{})

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != image {
		t.Errorf("image %q après une édition sans fichier choisi, %q attendue", apres, image)
	}
}

// Les deux bornes du schéma se voient dans le formulaire, pas dans une 500 :
// un téléversement refusé est une erreur de saisie ordinaire.
func TestUnTeleversementRefuseNeCreeRien(t *testing.T) {
	trop := append(pngDeTest(t), make([]byte, 6<<20)...)

	cas := []struct {
		nom     string
		fichier fichierPoste
	}{
		{"au-delà de 5 Mio", fichierPoste{nom: "enorme.png", contenu: trop}},
		{"type MIME hors liste", fichierPoste{nom: "notes.txt", contenu: []byte("ceci n'est pas une image")}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux, cookie := serveurConnecte(t)

			rec := poste(t, mux, "/recettes", cookie, champsValides(), c.fichier)
			corps := rec.Body.String()

			if rec.Code != http.StatusOK {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
			}
			if n := len(recettes(t, app)); n != 0 {
				t.Errorf("%d recettes créées malgré le téléversement refusé, 0 attendue", n)
			}
			if n := nombreDIngredients(t, app); n != 0 {
				t.Errorf("%d ingrédients créés malgré le téléversement refusé, 0 attendu", n)
			}
			if !strings.Contains(corps, "<form") {
				t.Errorf("le formulaire n'a pas été re-rendu :\n%s", corps)
			}
			if !strings.Contains(strings.ToLower(corps), "image") {
				t.Errorf("message d'erreur sans le nom du champ fautif :\n%s", corps)
			}
		})
	}
}

// --- Échappement et texte brut --------------------------------------------

// DOD.md §3 : une recette est du contenu étranger par nature, y compris quand
// c'est son propre auteur qui l'a tapée.
func TestLeTitreRessortEchappeDansLeFormulaireDEdition(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnregistree(t, app, map[string]any{"title": `<script>alert(1)</script>`})

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("balise script non échappée :\n%s", corps)
	}
	valeur := regexp.MustCompile(`name="titre"[^>]*value="([^"]*)"`).FindStringSubmatch(corps)
	if valeur == nil {
		t.Fatalf("attribut value du titre introuvable :\n%s", corps)
	}
	if !strings.Contains(valeur[1], "&lt;script&gt;") {
		t.Errorf("titre non échappé dans l'attribut : %q", valeur[1])
	}
}

// instructions est du texte brut : ce qui est saisi est stocké tel quel et
// ressort échappé. Le champ est un EditorField au schéma, mais rien n'y verse
// de HTML — l'éditeur riche et son assainissement sont une décision à part.
func TestInstructionsRestentDuTexteBrut(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	champs := champsValides()
	champs.Set("instructions", "Battre <b>vivement</b> & servir.")
	poste(t, mux, "/recettes", cookie, champs)

	recette := laRecette(t, app)
	if instructions := recette.GetString("instructions"); instructions != "Battre <b>vivement</b> & servir." {
		t.Errorf("instructions %q, attendues telles quelles", instructions)
	}

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()
	if strings.Contains(corps, "<b>vivement</b>") {
		t.Errorf("les instructions ressortent interprétées comme du HTML :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;b&gt;vivement&lt;/b&gt;") {
		t.Errorf("les instructions ne ressortent pas échappées :\n%s", corps)
	}
	if !strings.Contains(corps, "&amp; servir.") {
		t.Errorf("esperluette non échappée :\n%s", corps)
	}
}

// L'échappement d'un gabarit se contourne par un seul appel. Les deux portes
// se ferment ici, en une assertion qu'aucune relecture ne peut oublier : ni
// template.HTML dans le code, ni la fonction raw dans un gabarit.
func TestAucunGabaritNeContourneLEchappement(t *testing.T) {
	fichiers, err := vues.ReadDir("vues")
	if err != nil {
		t.Fatalf("liste des gabarits : %v", err)
	}

	appelRaw := regexp.MustCompile(`{{[^}]*\braw\b`)
	for _, fichier := range fichiers {
		contenu, err := vues.ReadFile("vues/" + fichier.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", fichier.Name(), err)
		}
		if appelRaw.Match(contenu) {
			t.Errorf("vues/%s emploie la fonction de gabarit raw", fichier.Name())
		}
	}

	if sources := chercheDansLesSources(t, "template.HTML"); len(sources) > 0 {
		t.Errorf("template.HTML employé dans %v", sources)
	}
}

// chercheDansLesSources rend les fichiers .go du paquet qui contiennent le
// motif donné.
func chercheDansLesSources(t *testing.T, motif string) []string {
	t.Helper()

	entrees, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("liste des sources : %v", err)
	}

	var trouves []string
	for _, entree := range entrees {
		// Les fichiers de test sont écartés : celui-ci nomme le motif
		// qu'il cherche, et se trouverait lui-même.
		if entree.IsDir() || !strings.HasSuffix(entree.Name(), ".go") || strings.HasSuffix(entree.Name(), "_test.go") {
			continue
		}
		contenu, err := os.ReadFile(entree.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", entree.Name(), err)
		}
		if bytes.Contains(contenu, []byte(motif)) {
			trouves = append(trouves, entree.Name())
		}
	}
	return trouves
}

// --- Recette introuvable ---------------------------------------------------

// Le contrôle de session passe avant la recherche de la recette : un visiteur
// ne doit pas apprendre, par un 404, quels identifiants existent.
func TestUnVisiteurSurUneRecetteInconnueVaALaConnexion(t *testing.T) {
	_, mux, _ := serveurConnecte(t)

	rec := avecCookie(mux, http.MethodGet, "/recettes/inexistante/modifier", nil)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if destination := rec.Header().Get("Location"); destination != "/connexion" {
		t.Errorf("Location %q, attendu %q", destination, "/connexion")
	}
}

// Un identifiant qui ne désigne rien est un 404, pas une erreur serveur — et
// une édition sur cet identifiant n'invente pas la recette manquante.
func TestUneRecetteInconnueEstUn404(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	rec := avecCookie(mux, http.MethodGet, "/recettes/inexistante/modifier", cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}

	rec = poste(t, mux, "/recettes/inexistante", cookie, champsValides())
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if n := len(recettes(t, app)); n != 0 {
		t.Errorf("%d recettes créées par une édition sur un identifiant inconnu, 0 attendue", n)
	}
}
