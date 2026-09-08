package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// --- Le montage -----------------------------------------------------------

// serveurConnecte monte le serveur de test et rend le cookie d'une session
// ouverte : la fiche exige un compte connecté, et tous les tests d'affichage
// passeraient sinon par la page de connexion.
func serveurConnecte(t *testing.T) (core.App, http.Handler, *http.Cookie) {
	t.Helper()

	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

	return app, mux, cookie
}

// fiche demande la fiche d'une recette et rend la réponse.
func fiche(mux http.Handler, cookie *http.Cookie, id string) *httptest.ResponseRecorder {
	return avecCookie(mux, http.MethodGet, "/recettes/"+id, cookie)
}

// recetteEnBase enregistre une recette, les champs donnés par-dessus un titre
// par défaut : la plupart des tests ne portent que sur un champ à la fois.
func recetteEnBase(t *testing.T, app core.App, champs map[string]any) *core.Record {
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

// recetteEnBaseSansValidation écrit la recette en court-circuitant la
// validation des champs.
//
// source_url est un URLField : « javascript:alert(1) » comme une suite
// d'espaces y sont refusés à l'écriture, et ces valeurs ne peuvent donc pas
// arriver par le formulaire. L'affichage ne doit pas s'appuyer là-dessus pour
// autant — une migration, un import ou une écriture directe en base les y
// mettent sans passer par le validateur, et c'est la page qui est alors le
// dernier rempart. Ces tests-là posent l'état que la validation interdit,
// précisément pour éprouver ce rempart.
func recetteEnBaseSansValidation(t *testing.T, app core.App, champs map[string]any) *core.Record {
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
	if err := app.SaveNoValidate(recette); err != nil {
		t.Fatalf("enregistrement sans validation : %v", err)
	}
	return recette
}

// champsDuParser sont les colonnes que le moteur d'analyse alimente, et les
// seules dont la fiche tire son mode d'affichage.
var champsDuParser = map[string]any{
	"quantity": nil,
	"unit":     "",
	"food":     "",
	"note":     "",
	"optional": false,
}

// ligneEnBase enregistre un ingrédient rattaché à la recette, avec exactement
// les colonnes de parser demandées — vides par défaut.
//
// Deux écritures, et c'est le cœur du montage : le hook d'ingredients.go
// remplit quantity, unit, food, note et optional depuis raw à la création,
// quoi qu'on lui passe. Il ne recalcule plus tant que raw n'a pas bougé — le
// chemin d'une correction manuelle — et c'est par lui que ces tests posent
// l'état qu'ils veulent éprouver. Sans ça, le repli sur raw dépendrait de ce
// que le moteur sait lire, c'est-à-dire de PATA-6 et du pack de langue.
func ligneEnBase(t *testing.T, app core.App, recette *core.Record, champs map[string]any) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatalf("collection ingredients : %v", err)
	}

	ligne := core.NewRecord(collection)
	ligne.Set("recipe", recette.Id)
	ligne.Set("raw", champs["raw"])
	ligne.Set("position", champs["position"])
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement de l'ingrédient : %v", err)
	}

	relue := relit(t, app, ligne.Id)
	for nom, defaut := range champsDuParser {
		valeur, donne := champs[nom]
		if !donne {
			valeur = defaut
		}
		relue.Set(nom, valeur)
	}
	if err := app.Save(relue); err != nil {
		t.Fatalf("pose des colonnes de parser : %v", err)
	}
	return relue
}

// tagEnBase crée un tag et rend son identifiant.
func tagEnBase(t *testing.T, app core.App, nom string) string {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}

	tag := core.NewRecord(collection)
	tag.Set("name", nom)
	if err := app.Save(tag); err != nil {
		t.Fatalf("enregistrement du tag %q : %v", nom, err)
	}
	return tag.Id
}

// premierTypeDePlat rend un type de plat parmi ceux que la migration initiale
// installe : la fiche n'en crée pas, elle affiche celui que la recette porte.
func premierTypeDePlat(t *testing.T, app core.App) *core.Record {
	t.Helper()

	types, err := app.FindRecordsByFilter("meal_types", "slug = 'dessert'", "", 1, 0)
	if err != nil || len(types) == 0 {
		t.Fatalf("type de plat « dessert » introuvable : %v", err)
	}
	return types[0]
}

// imageMinimale rend un PNG d'un pixel : le champ image n'accepte qu'un type
// d'image, et un fichier fabriqué à la main ne passerait pas la validation.
func imageMinimale(t *testing.T) *filesystem.File {
	t.Helper()

	var tampon bytes.Buffer
	if err := png.Encode(&tampon, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encodage du PNG de test : %v", err)
	}

	fichier, err := filesystem.NewFileFromBytes(tampon.Bytes(), "photo.png")
	if err != nil {
		t.Fatalf("fichier de test : %v", err)
	}
	return fichier
}

// --- La lecture du HTML rendu ---------------------------------------------

var (
	elementDeListe   = regexp.MustCompile(`(?s)<li[^>]*>.*?</li>`)
	baliseOuvrante   = regexp.MustCompile(`(?s)^<li[^>]*>`)
	contenuDeElement = regexp.MustCompile(`(?s)^<li[^>]*>(.*)</li>$`)

	// Le href tel que le navigateur le lirait : la casse ne le protège pas, et
	// un espace de tête est ignoré par les navigateurs.
	hrefEnJavascript = regexp.MustCompile(`(?i)href="\s*javascript:`)
	lienDeLaSource   = regexp.MustCompile(`(?s)<a[^>]*>`)
)

// lignesRenduesDe rend les <li> de la liste portant cette classe, dans l'ordre.
//
// Le nom dit « rendues » parce que recettes_test.go a son propre lignesDe, qui
// lit les ingrédients en base : ici on lit le HTML servi, pas les colonnes.
//
// Une lecture textuelle, et non un arbre DOM : ce qui est en jeu est ce que le
// navigateur reçoit, y compris les attributs, et un analyseur indulgent
// recollerait justement ce qu'un test d'échappement doit voir cassé.
func lignesRenduesDe(t *testing.T, corps, classe, fermante string) []string {
	t.Helper()

	bloc := entreBalises(corps, `class="`+classe+`"`, fermante)
	if bloc == "" {
		t.Fatalf("aucune liste de classe %q dans :\n%s", classe, corps)
	}
	return elementDeListe.FindAllString(bloc, -1)
}

// ingredientsRendus rend les lignes d'ingrédients du HTML servi — à ne pas
// confondre avec l'ingredientsDe de recettes_test.go, qui lit la base.
func ingredientsRendus(t *testing.T, corps string) []string {
	t.Helper()
	return lignesRenduesDe(t, corps, "ingredients", "</ul>")
}

// etapesDe rend les étapes rendues.
func etapesDe(t *testing.T, corps string) []string {
	t.Helper()
	return lignesRenduesDe(t, corps, "etapes", "</ol>")
}

// blocSource rend le paragraphe de la source, ou "" s'il n'y en a pas.
//
// Lecture textuelle comme le reste : ce qui est en jeu est ce que le navigateur
// reçoit, attributs compris.
func blocSource(corps string) string {
	return entreBalises(corps, "<p>Source : ", "</p>")
}

// contenu rend l'intérieur d'un <li>, attributs exclus.
func contenu(ligne string) string {
	trouve := contenuDeElement.FindStringSubmatch(ligne)
	if len(trouve) != 2 {
		return ""
	}
	return trouve[1]
}

// --- La route -------------------------------------------------------------

func TestLaFicheRendLaRecetteAUnCompteConnecte(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"title": "Tarte aux pommes"})

	rec := fiche(mux, cookie, recette.Id)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if corps := rec.Body.String(); !strings.Contains(corps, "Tarte aux pommes") {
		t.Errorf("fiche sans le titre de la recette :\n%s", corps)
	}
}

func TestUnIdentifiantInconnuRendUnePageIntrouvable(t *testing.T) {
	_, mux, cookie := serveurConnecte(t)

	rec := fiche(mux, cookie, "identifiantquinexiste")
	corps := rec.Body.String()

	if rec.Code != http.StatusNotFound {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(corps, "<html") {
		t.Errorf("page introuvable sans document complet :\n%s", corps)
	}
	if !strings.Contains(corps, "introuvable") {
		t.Errorf("page introuvable sans texte français explicite :\n%s", corps)
	}
}

// Un test par sens (§ Critères d'acceptation) : celui-ci dit ce qu'un visiteur
// n'obtient pas, TestLaFicheRendLaRecetteAUnCompteConnecte ce qu'un compte
// connecté obtient.
func TestLaFicheSansSessionRenvoieVersLaPageDeConnexion(t *testing.T) {
	app, mux, _ := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"title": "Tarte aux pommes"})

	rec := fiche(mux, nil, recette.Id)

	if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
		t.Errorf("Location %q, attendue %q", lieu, "/connexion")
	}
	if corps := rec.Body.String(); strings.Contains(corps, "Tarte aux pommes") {
		t.Errorf("le titre de la recette est servi à un visiteur :\n%s", corps)
	}
}

// Le contrôle de session passe avant la recherche de l'enregistrement : sinon
// la réponse dirait à un visiteur quels identifiants existent.
func TestSansSessionLaFicheNeDitPasSiLaRecetteExiste(t *testing.T) {
	app, mux, _ := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	existante := fiche(mux, nil, recette.Id)
	inconnue := fiche(mux, nil, "identifiantquinexiste")

	if existante.Code != inconnue.Code {
		t.Errorf("statuts distincts : %d sur une recette existante, %d sur un identifiant inconnu",
			existante.Code, inconnue.Code)
	}
	if inconnue.Header().Get("Location") != "/connexion" {
		t.Errorf("identifiant inconnu : Location %q, attendue %q",
			inconnue.Header().Get("Location"), "/connexion")
	}
}

// --- Les ingrédients ------------------------------------------------------

func TestLesIngredientsSortentTousDansLOrdreDeLeurPosition(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	// Enregistrées à l'envers : un test qui les poserait dans l'ordre passerait
	// même sans tri.
	for _, position := range []int{5, 3, 1, 4, 2} {
		ligneEnBase(t, app, recette, map[string]any{
			"raw":      "ingrédient numéro " + string(rune('0'+position)),
			"position": position,
		})
	}

	corps := fiche(mux, cookie, recette.Id).Body.String()
	lignes := ingredientsRendus(t, corps)

	if len(lignes) != 5 {
		t.Fatalf("%d ingrédients rendus, attendus 5 :\n%s", len(lignes), corps)
	}
	for i, ligne := range lignes {
		attendu := "ingrédient numéro " + string(rune('1'+i))
		if !strings.Contains(ligne, attendu) {
			t.Errorf("ligne %d : %q, attendue contenant %q", i+1, ligne, attendu)
		}
	}
}

func TestUnIngredientSansAlimentSAfficheParSaLigneBrute(t *testing.T) {
	const brut = "1 pincée de fleur de sel, ou ce que vous avez"

	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{"raw": brut, "position": 1})

	corps := fiche(mux, cookie, recette.Id).Body.String()
	lignes := ingredientsRendus(t, corps)

	if len(lignes) != 1 {
		t.Fatalf("%d ingrédients rendus, attendu 1 :\n%s", len(lignes), corps)
	}
	if got := strings.TrimSpace(contenu(lignes[0])); got != brut {
		t.Errorf("ligne rendue %q, attendue à l'identique %q", got, brut)
	}
}

// Un aliment réduit à des espaces n'est pas un aliment reconnu : le mode
// structuré rendrait un <span class="aliment"> vide, là où raw porte encore
// toute la ligne. C'est « non vide une fois les espaces rognés », pris au mot.
func TestUnIngredientDontLAlimentNEstQueDesEspacesSAfficheParSaLigneBrute(t *testing.T) {
	const brut = "2 cuillères à soupe de crème, ou de yaourt"

	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{"raw": brut, "position": 1, "food": "   "})

	corps := fiche(mux, cookie, recette.Id).Body.String()
	lignes := ingredientsRendus(t, corps)

	if len(lignes) != 1 {
		t.Fatalf("%d ingrédients rendus, attendu 1 :\n%s", len(lignes), corps)
	}
	if got := strings.TrimSpace(contenu(lignes[0])); got != brut {
		t.Errorf("ligne rendue %q, attendue à l'identique %q", got, brut)
	}
}

func TestUnIngredientAvecAlimentSAfficheEnStructure(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "200 g de farine de sarrasin, bio",
		"position": 1,
		"quantity": 200,
		"unit":     "g",
		"food":     "farine de sarrasin",
		"note":     "bio",
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()
	ligne := ingredientsRendus(t, corps)[0]

	for _, attendu := range []string{"200", "g", "farine de sarrasin"} {
		if !strings.Contains(ligne, attendu) {
			t.Errorf("ligne structurée sans %q : %q", attendu, ligne)
		}
	}

	note := entreBalises(ligne, `<span class="note">`, "</span>")
	if !strings.Contains(note, "bio") {
		t.Errorf("la note ne sort pas dans son propre élément : %q", ligne)
	}
	if strings.Contains(note, "farine de sarrasin") {
		t.Errorf("la note et l'aliment sortent dans le même élément : %q", ligne)
	}
}

func TestUnIngredientSansNoteNeRendPasDElementDeNoteVide(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "200 g de farine",
		"position": 1,
		"quantity": 200,
		"unit":     "g",
		"food":     "farine",
	})

	ligne := ingredientsRendus(t, fiche(mux, cookie, recette.Id).Body.String())[0]

	if strings.Contains(ligne, `class="note"`) {
		t.Errorf("élément de note rendu alors que la note est vide : %q", ligne)
	}
}

// « La mise en forme peut se dégrader, l'information ne disparaît jamais » :
// raw reste dans la page, y compris quand le parser a su lire la ligne.
func TestChaqueLigneDIngredientPorteSaLigneBrute(t *testing.T) {
	const brutStructure = "200 g de farine de sarrasin, bio, tamisée deux fois"
	const brutNonLu = "un peu de tout ce qui traîne"

	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      brutStructure,
		"position": 1,
		"quantity": 200,
		"unit":     "g",
		"food":     "farine de sarrasin",
	})
	ligneEnBase(t, app, recette, map[string]any{"raw": brutNonLu, "position": 2})

	lignes := ingredientsRendus(t, fiche(mux, cookie, recette.Id).Body.String())

	for i, brut := range []string{brutStructure, brutNonLu} {
		if !strings.Contains(lignes[i], `title="`+brut+`"`) {
			t.Errorf("ligne %d sans sa ligne brute en attribut title : %q", i+1, lignes[i])
		}
	}
}

func TestUnIngredientFacultatifPorteLaMention(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "1 pincée de cannelle",
		"position": 1,
		"food":     "cannelle",
		"optional": true,
	})
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "200 g de farine",
		"position": 2,
		"food":     "farine",
	})

	lignes := ingredientsRendus(t, fiche(mux, cookie, recette.Id).Body.String())

	if !strings.Contains(lignes[0], "(facultatif)") {
		t.Errorf("ingrédient facultatif sans mention lisible : %q", lignes[0])
	}
	if strings.Contains(lignes[1], "(facultatif)") {
		t.Errorf("ingrédient obligatoire portant la mention « (facultatif) » : %q", lignes[1])
	}
}

// --- Les instructions -----------------------------------------------------

func TestLesInstructionsRendentUneEtapeParLigneNonVide(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{
		"instructions": "Éplucher les pommes.\n\nÉtaler la pâte.\nEnfourner 30 minutes.\n",
	})

	etapes := etapesDe(t, fiche(mux, cookie, recette.Id).Body.String())

	if len(etapes) != 3 {
		t.Fatalf("%d étapes rendues, attendues 3 : %q", len(etapes), etapes)
	}
	for i, attendu := range []string{"Éplucher les pommes.", "Étaler la pâte.", "Enfourner 30 minutes."} {
		if got := strings.TrimSpace(contenu(etapes[i])); got != attendu {
			t.Errorf("étape %d : %q, attendue %q", i+1, got, attendu)
		}
	}
}

func TestDesInstructionsVidesNeRendentNiLibelleNiListe(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"instructions": ""})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	if strings.Contains(corps, "Instructions") {
		t.Errorf("libellé des instructions rendu sur une recette sans instructions :\n%s", corps)
	}
	if strings.Contains(corps, `class="etapes"`) {
		t.Errorf("liste d'étapes rendue sur une recette sans instructions :\n%s", corps)
	}
}

// « instructions est du texte, pas du HTML » (tranché le 19/08/2026), dans sa
// formulation binaire : les balises stockées s'affichent, elles ne s'exécutent
// pas.
func TestLesInstructionsSontDuTexteEtNonDuHTML(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{
		"instructions": "Battre le <b>gras</b> avec le sucre.",
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	if strings.Contains(corps, "<b>gras</b>") {
		t.Errorf("balise <b> issue de la donnée dans le HTML rendu :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;b&gt;gras&lt;/b&gt;") {
		t.Errorf("le texte des instructions ne s'affiche pas tel quel :\n%s", corps)
	}
}

// La fonction raw est enregistrée d'office par le registre de PocketBase : elle
// est donc à portée de main dans chaque gabarit, et rien ne signalerait son
// emploi. Ce test-là le signale.
func TestAucunGabaritNutiliseLaFonctionRaw(t *testing.T) {
	action := regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	appelDeRaw := regexp.MustCompile(`\braw\b`)

	fichiers, err := vues.ReadDir("vues")
	if err != nil {
		t.Fatalf("liste des gabarits : %v", err)
	}

	for _, fichier := range fichiers {
		gabarit, err := vues.ReadFile("vues/" + fichier.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", fichier.Name(), err)
		}
		for _, expression := range action.FindAllString(string(gabarit), -1) {
			if appelDeRaw.MatchString(expression) {
				t.Errorf("vues/%s appelle la fonction raw : %s", fichier.Name(), expression)
			}
		}
	}
}

// --- Les champs facultatifs -----------------------------------------------

func TestUneRecetteDepouilleeNeRendAucunLibelle(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"title": "Pain perdu"})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	for _, libelle := range []string{
		"Portions",
		"Temps de préparation",
		"Temps de cuisson",
		"Type de plat",
		"Saisons",
		"Tags",
		"Source",
		"Ajoutée par",
		"Ingrédients",
		"Instructions",
	} {
		if strings.Contains(corps, libelle) {
			t.Errorf("libellé %q rendu alors que la donnée manque :\n%s", libelle, corps)
		}
	}
	if strings.Contains(corps, "<img") {
		t.Errorf("cadre d'image rendu sur une recette sans image :\n%s", corps)
	}
	if !strings.Contains(corps, "Pain perdu") {
		t.Errorf("fiche dépouillée sans son titre :\n%s", corps)
	}
}

func TestLesChampsRenseignesSontRendusAvecLeurLibelle(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	typeDePlat := premierTypeDePlat(t, app)

	// Un second compte, et non celui de la session : le nom du compte connecté
	// figure déjà dans l'en-tête de chaque page, et l'assertion passerait sans
	// que le bloc « Ajoutée par » existe.
	auteur := creeCompte(t, app, "marguerite@exemple.fr", "Marguerite")

	recette := recetteEnBase(t, app, map[string]any{
		"servings":   6,
		"prep_time":  45,
		"cook_time":  30,
		"meal_type":  typeDePlat.Id,
		"seasons":    []string{"automne", "hiver"},
		"tags":       []string{tagEnBase(t, app, "végétarien")},
		"created_by": auteur.Id,
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	for _, attendu := range []string{
		"Portions", "6",
		"Temps de préparation", "45 min",
		"Temps de cuisson", "30 min",
		"Type de plat", typeDePlat.GetString("name"),
		"Saisons", "automne", "hiver",
		"Tags", "végétarien",
		"Ajoutée par", "Marguerite",
	} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("fiche sans %q :\n%s", attendu, corps)
		}
	}
}

// TestUnAuteurSansNomNePubliePasSonCourriel : la fiche est lisible par tout
// compte connecté, et l'auteur d'une recette est un autre compte que son
// lecteur. PocketBase protège cette adresse partout ailleurs — users porte
// ViewRule = id = @request.auth.id et emailVisibility vaut faux —, mais
// l'expansion de created_by court-circuite la règle : c'est donc à la fiche de
// ne pas la publier.
//
// Le sens inverse — un auteur nommé sort sous son nom — est couvert par
// TestLesChampsRenseignesSontRendusAvecLeurLibelle, qui l'affirme déjà sur
// « Marguerite ». Un second test le redirait, et retirer le bloc ferait rougir
// deux tests au lieu d'un (DOD.md §2).
func TestUnAuteurSansNomNePubliePasSonCourriel(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)

	const courrielDeLAuteur = "sans-nom@exemple.fr"
	auteur := creeCompte(t, app, courrielDeLAuteur, "")
	recette := recetteEnBase(t, app, map[string]any{"created_by": auteur.Id})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	if strings.Contains(corps, courrielDeLAuteur) {
		t.Errorf("le courriel de l'auteur est publié à un autre compte :\n%s", corps)
	}
	// Faute de nom à afficher, le bloc disparaît comme n'importe quelle donnée
	// manquante — c'est la règle du point 5 de la tâche, pas une exception.
	if strings.Contains(corps, "Ajoutée par") {
		t.Errorf("bloc « Ajoutée par » rendu alors qu'il n'y a pas de nom :\n%s", corps)
	}
}

func TestLImageEstServieParSaMiniature(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"image": imageMinimale(t)})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	attendu := "/api/files/recipes/" + recette.Id + "/" + recette.GetString("image") + "?thumb=800x0"
	if !strings.Contains(corps, `src="`+attendu+`"`) {
		t.Errorf("fiche sans la miniature %q :\n%s", attendu, corps)
	}
}

// --- La source d'origine --------------------------------------------------

func TestLaSourceEstUnLienVersLeSiteDOrigine(t *testing.T) {
	const adresse = "https://exemple.fr/tarte-aux-pommes"

	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{
		"source_url":  adresse,
		"source_name": "Exemple",
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	lien := entreBalises(corps, `<a href="`+adresse+`"`, "</a>")
	if lien == "" {
		t.Fatalf("aucun lien vers %q :\n%s", adresse, corps)
	}
	if !strings.Contains(lien, "Exemple") {
		t.Errorf("le lien de source ne porte pas le nom du site : %q", lien)
	}
}

// libelleSource décide seule de ce qui s'affiche ; le gabarit ne fait que le
// rendre. C'est ce qui permet de couvrir les cas limites sans monter de base,
// et notamment ceux que la validation de source_url interdit d'écrire.
func TestLeLibelleDeLaSourceSuitLeNomPuisLeDomaine(t *testing.T) {
	cas := []struct {
		nom, adresse, attendu string
	}{
		// Le nom du site prime, et sort tel quel.
		{"Marmiton", "https://www.marmiton.org/recettes/x", "Marmiton"},
		// Faute de nom — le cas courant tant que rien ne l'extrait —, l'hôte,
		// sans son www. et en minuscules.
		{"", "https://www.marmiton.org/recettes/x", "marmiton.org"},
		{"   ", "https://www.marmiton.org/recettes/x", "marmiton.org"},
		{"", "https://WWW.Marmiton.ORG/x", "marmiton.org"},
		// Un hôte qui ne commence pas par www. n'est pas amputé pour autant.
		{"", "https://cuisine.journaldesfemmes.fr/x", "cuisine.journaldesfemmes.fr"},
		// Aucun hôte dérivable : l'adresse telle quelle. On préfère un libellé
		// laid à une source disparue — la même règle que le repli sur la ligne
		// brute d'un ingrédient.
		{"", "pas-une-url", "pas-une-url"},
		// Rien à afficher : c'est le bloc entier qui disparaîtra.
		{"", "", ""},
		{"   ", "   ", ""},
	}

	for _, c := range cas {
		if got := libelleSource(c.nom, c.adresse); got != c.attendu {
			t.Errorf("libelleSource(%q, %q) = %q, attendu %q", c.nom, c.adresse, got, c.attendu)
		}
	}
}

// Le repli sur le domaine n'est pas un cas dégradé : rien n'extrait le nom du
// site aujourd'hui, donc c'est ce que la plupart des recettes importées
// afficheront.
func TestUneSourceSansNomDeSiteSAfficheParSonDomaine(t *testing.T) {
	const adresse = "https://www.marmiton.org/recettes/tarte-aux-pommes"

	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"source_url": adresse})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	// L'ancre est cherchée sur le href complet : c'est aussi l'assertion que
	// l'adresse d'origine sort inchangée, et non réduite au domaine.
	lien := entreBalises(corps, `<a href="`+adresse+`"`, "</a>")
	if lien == "" {
		t.Fatalf("aucun lien vers %q :\n%s", adresse, corps)
	}
	if !strings.Contains(lien, ">marmiton.org") {
		t.Errorf("le lien ne porte pas le domaine en repli : %q", lien)
	}
	if strings.Contains(lien, "www.") {
		t.Errorf("le préfixe www. subsiste dans le libellé : %q", lien)
	}
}

// Une source sans adresse est celle d'un carnet de famille ou d'un livre : le
// nom se lit, mais il n'y a rien où aller.
//
// Une adresse réduite à des espaces est le même cas : elle ne mène nulle part,
// et un href fait d'espaces serait un lien mort que rien ne distingue à l'œil
// d'un lien vivant.
func TestUneSourceSansAdresseSAfficheSansLien(t *testing.T) {
	cas := map[string]map[string]any{
		"adresse absente":   {"source_name": "Le carnet de Mamie"},
		"adresse d'espaces": {"source_name": "Le carnet de Mamie", "source_url": "   "},
	}

	for nom, champs := range cas {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := serveurConnecte(t)
			// Sans validation : une adresse d'espaces est refusée à
			// l'écriture, et l'affichage ne doit pas s'appuyer là-dessus.
			recette := recetteEnBaseSansValidation(t, app, champs)

			bloc := blocSource(fiche(mux, cookie, recette.Id).Body.String())

			if !strings.Contains(bloc, "Le carnet de Mamie") {
				t.Fatalf("le nom de la source ne figure pas dans la page : %q", bloc)
			}
			if lienDeLaSource.MatchString(bloc) {
				t.Errorf("lien mort rendu alors qu'aucune adresse n'est connue : %q", bloc)
			}
		})
	}
}

// Les deux champs vides sont déjà couverts par
// TestUneRecetteDepouilleeNeRendAucunLibelle, qui exige l'absence du libellé
// « Source » sur une recette nue. Ici, la même exigence pour ce que la validation
// ne rogne pas : des champs qui ne portent que des espaces.
func TestUneSourceReduiteADesEspacesNeRendAucunLibelle(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBaseSansValidation(t, app, map[string]any{
		"source_url":  "   ",
		"source_name": "   ",
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	if strings.Contains(corps, "Source") {
		t.Errorf("libellé « Source » rendu pour des champs vides d'espaces :\n%s", corps)
	}
}

// L'adresse d'une source vient d'un site tiers, et un schéma javascript: y
// donnerait un lien exécutable dans une page servie sous session. Le critère
// porte sur ce qui sort : peu importe que ce soit html/template qui neutralise
// ou le code qui refuse le schéma.
func TestUneAdresseDeSourceEnJavascriptNeSortAucunHrefExecutable(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	// Le nom du site est renseigné pour que le bloc existe sans rien devoir au
	// repli sur le domaine : ce test éprouve le href, et lui seul.
	recette := recetteEnBaseSansValidation(t, app, map[string]any{
		"source_url":  "javascript:alert(1)",
		"source_name": "Exemple",
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	// Le bloc doit exister : sans lui, l'absence de href se vérifierait toute
	// seule et le test ne dirait plus rien.
	bloc := blocSource(corps)
	if !lienDeLaSource.MatchString(bloc) {
		t.Fatalf("aucun lien de source rendu, le test ne prouve rien :\n%s", corps)
	}
	if hrefEnJavascript.MatchString(corps) {
		t.Errorf("href exécutable rendu dans la page :\n%s", corps)
	}
}

// Un guillemet double dans l'adresse refermerait l'attribut href et tout ce qui
// suit deviendrait des attributs — la même menace que sur l'attribut title
// d'une ligne d'ingrédient.
func TestUnGuillemetDansLAdresseDeSourceNeRefermePasLAttribut(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	// Sans validation : le validateur refuse déjà cette adresse, et c'est
	// justement ce qu'on ne veut pas prendre pour un rempart d'affichage.
	recette := recetteEnBaseSansValidation(t, app, map[string]any{
		"source_url":  `https://exemple.fr/x" onmouseover="alert(1)`,
		"source_name": "Exemple",
	})

	bloc := blocSource(fiche(mux, cookie, recette.Id).Body.String())
	ouvrante := lienDeLaSource.FindString(bloc)
	if ouvrante == "" {
		t.Fatalf("aucun lien de source rendu : %q", bloc)
	}

	// Quatre guillemets doubles, et quatre seulement : ceux qui bornent href et
	// rel. Un cinquième serait un guillemet venu de la donnée, donc l'attribut
	// refermé et onmouseover devenu un vrai attribut.
	if guillemets := strings.Count(ouvrante, `"`); guillemets != 4 {
		t.Errorf("%d guillemets doubles dans la balise du lien, attendus 4 : %q", guillemets, ouvrante)
	}
	if strings.Contains(ouvrante, "onmouseover=\"") {
		t.Errorf("attribut exécutable reconstitué depuis l'adresse : %q", ouvrante)
	}
}

// --- Les quantités --------------------------------------------------------

func TestLesQuantitesSAffichentEnFrancais(t *testing.T) {
	cas := []struct {
		quantite float64
		attendu  string
	}{
		{200, "200"},
		// Virgule décimale : la fiche se lit en français, et « 0.5 » y est une
		// faute d'orthographe autant qu'un anglicisme.
		{0.5, "0,5"},
		{1.25, "1,25"},
		// Pas de zéro décimal traînant sur une quantité entière.
		{3, "3"},
		// Absente : rien du tout. quantity est facultatif et le restera pour
		// « une pincée de sel » ; un « 0 » collé devant chaque aliment serait le
		// cas courant tant que PATA-6 n'est pas branché.
		{0, ""},
	}

	for _, c := range cas {
		if got := quantiteLisible(c.quantite); got != c.attendu {
			t.Errorf("quantiteLisible(%v) = %q, attendu %q", c.quantite, got, c.attendu)
		}
	}
}

func TestUnIngredientSansQuantiteNAffichePasDeZero(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "une pincée de sel",
		"position": 1,
		"food":     "sel",
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()
	// Le contenu seul : la ligne brute est en attribut title, et c'est le texte
	// affiché qui est en jeu.
	ligne := contenu(ingredientsRendus(t, corps)[0])

	if strings.Contains(ligne, "0") {
		t.Errorf("quantité absente rendue par un « 0 » : %q", ligne)
	}
	if !strings.Contains(ligne, "sel") {
		t.Errorf("ligne structurée sans son aliment : %q", ligne)
	}
}

// --- Les durées -----------------------------------------------------------

func TestLesDureesSAffichentEnFrancais(t *testing.T) {
	cas := []struct {
		minutes int
		attendu string
	}{
		{45, "45 min"},
		{90, "1 h 30"},
		{120, "2 h"},
		// Le zéro de tête n'est pas un ornement : « 1 h 5 » se lit comme une
		// coquille, et la fiche est faite pour être lue en cuisinant.
		{65, "1 h 05"},
		{1, "1 min"},
		// Absente ou nulle : rien, et c'est le bloc entier qui disparaît.
		{0, ""},
	}

	for _, c := range cas {
		if got := dureeLisible(c.minutes); got != c.attendu {
			t.Errorf("dureeLisible(%d) = %q, attendu %q", c.minutes, got, c.attendu)
		}
	}
}

func TestUneDureeNulleFaitDisparaitreSonBloc(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"prep_time": 90, "cook_time": 0})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	if !strings.Contains(corps, "1 h 30") {
		t.Errorf("temps de préparation absent :\n%s", corps)
	}
	if strings.Contains(corps, "Temps de cuisson") {
		t.Errorf("libellé du temps de cuisson rendu alors qu'il vaut zéro :\n%s", corps)
	}
}

// --- L'échappement (DOD.md §3) --------------------------------------------

// Quatre emplacements, quatre chemins de rendu : titre, ingrédient, tag et
// instructions. Le test porte sur le HTML rendu, pas sur un appel
// d'échappement.
func TestLaFicheEchappeToutCeQuiVientDuDehors(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{
		"title":        `<script>alert(1)</script>`,
		"instructions": `<script>alert(4)</script>`,
		"tags":         []string{tagEnBase(t, app, `"><script>`)},
		// Le nom du site vient d'un tiers au même titre que le reste, et il
		// sort en texte dans le bloc de la source.
		"source_url":  "https://exemple.fr/x",
		"source_name": `<script>alert(5)</script>`,
	})
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      `<img src=x onerror=alert(1)>`,
		"position": 1,
	})

	corps := fiche(mux, cookie, recette.Id).Body.String()

	// Les formes exécutables, et non les sous-chaînes : « onerror= » figure
	// légitimement dans la page, en texte, puisque la ligne brute y est rendue
	// entière. Ce qui la rendrait exécutable est le chevron non échappé qui
	// ouvrirait ou refermerait une balise.
	for _, interdit := range []string{"<script>", "<img src=x", "onerror=alert(1)>"} {
		if strings.Contains(corps, interdit) {
			t.Errorf("%q ressort exécutable dans la page :\n%s", interdit, corps)
		}
	}
	for _, attendu := range []string{
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"&lt;script&gt;alert(4)&lt;/script&gt;",
		"&lt;script&gt;alert(5)&lt;/script&gt;",
		"&lt;img src=x onerror=alert(1)&gt;",
		"&#34;&gt;&lt;script&gt;",
	} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("%q absent de la page : l'information a disparu au lieu d'être échappée :\n%s", attendu, corps)
		}
	}
}

// L'attribut title porte une ligne venue d'un site tiers : un guillemet double
// qui refermerait l'attribut ouvrirait la porte à tout le reste.
func TestLAttributTitreDeLaLigneBruteEstEchappe(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      `1 cuillère de "vrai" beurre" onmouseover="alert(1)`,
		"position": 1,
	})

	ligne := ingredientsRendus(t, fiche(mux, cookie, recette.Id).Body.String())[0]
	ouvrante := baliseOuvrante.FindString(ligne)

	// Deux guillemets doubles dans la balise ouvrante, et deux seulement :
	// ceux qui bornent title. Un troisième serait un guillemet venu de la
	// donnée, donc l'attribut refermé et onmouseover devenu un vrai attribut.
	if guillemets := strings.Count(ouvrante, `"`); guillemets != 2 {
		t.Errorf("%d guillemets doubles dans la balise ouvrante, attendus 2 : %q", guillemets, ouvrante)
	}
	if !strings.Contains(ouvrante, "&#34;") {
		t.Errorf("guillemet double non échappé dans l'attribut title : %q", ouvrante)
	}
}
