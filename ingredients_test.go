package main

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/template"
)

// analyseurDeTest rend un analyseur chargé du pack français.
//
// Un analyseur par test, et non un partagé : le compteur de lignes non lues
// est un état, et deux tests qui se le passeraient dépendraient de leur ordre
// d'exécution. Le chargement coûte une trentaine de millisecondes.
func analyseurDeTest(t *testing.T) *analyseur {
	t.Helper()

	a, err := analyseurFR()
	if err != nil {
		t.Fatalf("chargement du pack français : %v", err)
	}
	return a
}

// baseNeuveAvec monte une base vide, y branche nos hooks avec l'analyseur
// donné, puis applique les migrations — dans l'ordre où main() le fait.
func baseNeuveAvec(t *testing.T, a *analyseur) core.App {
	t.Helper()

	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	brancheLesHooks(app, a)

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("amorçage : %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrations : %v", err)
	}
	return app
}

// recetteNeuve pose la recette à laquelle rattacher les lignes : ingredients
// exige une relation, un ingrédient orphelin n'existe pas.
func recetteNeuve(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", "Tarte aux pommes")
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette : %v", err)
	}
	return recette
}

// ingredientNeuf rend une ligne non enregistrée, rattachée à la recette.
func ingredientNeuf(t *testing.T, app core.App, recette *core.Record, brut string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatalf("collection ingredients : %v", err)
	}
	ligne := core.NewRecord(collection)
	ligne.Set("recipe", recette.Id)
	ligne.Set("raw", brut)
	return ligne
}

func nombreDIngredients(t *testing.T, app core.App) int {
	t.Helper()

	lignes, err := app.FindAllRecords("ingredients")
	if err != nil {
		t.Fatalf("lecture des ingrédients : %v", err)
	}
	return len(lignes)
}

// --- La conversion, sans base ni serveur ---------------------------------

// TestLaConversionRendLesCinqChamps fige la correspondance
// *moteur.Ingredient → enregistrement sur des sorties réelles du moteur.
//
// unit prend Unite.Abrev, seule des trois écritures à être à la fois
// canonique et affichable : UniteCle() rendrait « cuillere_a_soupe » et
// UniteTexte la graisse d'origine, qui ne regroupe rien.
func TestLaConversionRendLesCinqChamps(t *testing.T) {
	a := analyseurDeTest(t)

	nombre := func(v float64) *float64 { return &v }

	cas := []struct {
		brut      string
		quantite  *float64
		unite     string
		aliment   string
		note      string
		optionnel bool
	}{
		{"500 g de beurre demi-sel", nombre(500), "g", "beurre demi-sel", "", false},
		{"2 gousses d'ail dégermées (facultatif)", nombre(2), "gousse", "ail dégermées", "facultatif", true},
		{"1 c. à s. rase de sucre", nombre(1), "c. à s.", "sucre", "", false},
		{"sel", nil, "", "sel", "", false},
		// La borne haute n'est pas écrite : quantity porte la valeur nue.
		{"2 à 3 gousses d'ail", nombre(2), "gousse", "ail", "", false},
		// L'approximation n'est pas écrite non plus.
		{"environ 200 g de farine", nombre(200), "g", "farine", "", false},
		// Le moteur ne rend aucun nombre : on n'en invente pas.
		{"un peu de lait", nil, "", "lait", "", false},
	}
	for _, c := range cas {
		t.Run(c.brut, func(t *testing.T) {
			obtenus := champsLus(a.lit(c.brut))

			switch {
			case c.quantite == nil && obtenus.quantite != nil:
				t.Errorf("quantity = %v, attendu vide", *obtenus.quantite)
			case c.quantite != nil && obtenus.quantite == nil:
				t.Errorf("quantity vide, attendu %v", *c.quantite)
			case c.quantite != nil && *obtenus.quantite != *c.quantite:
				t.Errorf("quantity = %v, attendu %v", *obtenus.quantite, *c.quantite)
			}
			if obtenus.unite != c.unite {
				t.Errorf("unit = %q, attendu %q", obtenus.unite, c.unite)
			}
			if obtenus.aliment != c.aliment {
				t.Errorf("food = %q, attendu %q", obtenus.aliment, c.aliment)
			}
			if obtenus.note != c.note {
				t.Errorf("note = %q, attendu %q", obtenus.note, c.note)
			}
			if obtenus.optionnel != c.optionnel {
				t.Errorf("optional = %v, attendu %v", obtenus.optionnel, c.optionnel)
			}
		})
	}
}

// --- Le hook, à la création ----------------------------------------------

func TestLeHookRemplitLesChampsALaCreation(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	ligne := ingredientNeuf(t, app, recette, "500 g de beurre demi-sel")
	// Fournis dans la requête, et faux : le hook les recalcule quand même,
	// sinon un client mal intentionné choisirait ce que la fiche affiche.
	ligne.Set("quantity", 9999)
	ligne.Set("unit", "tonne")
	ligne.Set("food", "cyanure")
	ligne.Set("note", "à volonté")
	ligne.Set("optional", true)

	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement : %v", err)
	}

	if q := ligne.GetFloat("quantity"); q != 500 {
		t.Errorf("quantity = %v, attendu 500", q)
	}
	if u := ligne.GetString("unit"); u != "g" {
		t.Errorf("unit = %q, attendu %q", u, "g")
	}
	if f := ligne.GetString("food"); f != "beurre demi-sel" {
		t.Errorf("food = %q, attendu %q", f, "beurre demi-sel")
	}
	if n := ligne.GetString("note"); n != "" {
		t.Errorf("note = %q, attendu vide", n)
	}
	if o := ligne.GetBool("optional"); o {
		t.Error("optional = vrai, attendu faux")
	}
}

func TestUneLigneVideNEcritAucunEnregistrement(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	for _, brut := range []string{"", "   ", "\t\n"} {
		t.Run(strings.TrimSpace("vide "+brut), func(t *testing.T) {
			ligne := ingredientNeuf(t, app, recette, brut)
			if err := app.Save(ligne); err == nil {
				t.Fatalf("enregistrement de %q accepté, attendu refusé", brut)
			}
		})
	}

	if n := nombreDIngredients(t, app); n != 0 {
		t.Errorf("%d ligne(s) en base, attendu 0", n)
	}
}

// TestUneLigneIllisibleEcritQuandMemeUnEnregistrement tient la règle de
// conduite : un échec d'analyse dégrade la mise en forme, il ne fait jamais
// disparaître un ingrédient de la recette.
func TestUneLigneIllisibleEcritQuandMemeUnEnregistrement(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	fournies := []string{"500 g de beurre demi-sel", "???", "sel", "( )"}
	for _, brut := range fournies {
		ligne := ingredientNeuf(t, app, recette, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de %q : %v", brut, err)
		}
	}

	if n := nombreDIngredients(t, app); n != len(fournies) {
		t.Errorf("%d ligne(s) en base, attendu %d", n, len(fournies))
	}
}

// TestRawEstConserveMotPourMot : raw est le champ le plus important du schéma.
// Rien ne le rogne, ne le normalise ni ne le recalcule — pas même quand le
// moteur ne rend rien d'exploitable.
func TestRawEstConserveMotPourMot(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	bruts := []string{
		`2 c. à s. d'huile d'olive « vierge », zeste d'½ citron (bio)`,
		`sucre &amp; cannelle`,
		`  ???  `,
	}
	for _, brut := range bruts {
		t.Run(brut, func(t *testing.T) {
			ligne := ingredientNeuf(t, app, recette, brut)
			if err := app.Save(ligne); err != nil {
				t.Fatalf("enregistrement : %v", err)
			}

			relue, err := app.FindRecordById("ingredients", ligne.Id)
			if err != nil {
				t.Fatalf("relecture : %v", err)
			}
			if obtenu := relue.GetString("raw"); obtenu != brut {
				t.Errorf("raw = %q, attendu %q", obtenu, brut)
			}
		})
	}
}

// --- Le hook, à la mise à jour -------------------------------------------

// TestUneCorrectionManuelleSurvitATantQueRawNeBougePas : le recalcul
// systématique effacerait la correction de l'utilisateur à l'enregistrement
// suivant, ce qui vaut mieux que de ne pas offrir la correction du tout.
func TestUneCorrectionManuelleSurvitATantQueRawNeBougePas(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	ligne := ingredientNeuf(t, app, recette, "2 gousses d'ail dégermées (facultatif)")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement : %v", err)
	}

	ligne.Set("food", "ail dégermé")
	ligne.Set("quantity", 3)
	if err := app.Save(ligne); err != nil {
		t.Fatalf("ré-enregistrement : %v", err)
	}

	relue, err := app.FindRecordById("ingredients", ligne.Id)
	if err != nil {
		t.Fatalf("relecture : %v", err)
	}
	if f := relue.GetString("food"); f != "ail dégermé" {
		t.Errorf("food = %q, attendu %q — la correction a été écrasée", f, "ail dégermé")
	}
	if q := relue.GetFloat("quantity"); q != 3 {
		t.Errorf("quantity = %v, attendu 3 — la correction a été écrasée", q)
	}
}

func TestRawModifieRecalculeLesCinqChamps(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	ligne := ingredientNeuf(t, app, recette, "2 gousses d'ail dégermées (facultatif)")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement : %v", err)
	}
	ligne.Set("food", "ail dégermé")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("correction manuelle : %v", err)
	}

	ligne.Set("raw", "1 c. à s. rase de sucre")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("changement de raw : %v", err)
	}

	relue, err := app.FindRecordById("ingredients", ligne.Id)
	if err != nil {
		t.Fatalf("relecture : %v", err)
	}
	if q := relue.GetFloat("quantity"); q != 1 {
		t.Errorf("quantity = %v, attendu 1", q)
	}
	if u := relue.GetString("unit"); u != "c. à s." {
		t.Errorf("unit = %q, attendu %q", u, "c. à s.")
	}
	if f := relue.GetString("food"); f != "sucre" {
		t.Errorf("food = %q, attendu %q", f, "sucre")
	}
	if n := relue.GetString("note"); n != "" {
		t.Errorf("note = %q, attendu vide", n)
	}
	if o := relue.GetBool("optional"); o {
		t.Error("optional = vrai, attendu faux")
	}
}

// --- Le compteur ---------------------------------------------------------

// TestLeCompteurCompteLesLignesNonLues : est « non lue » une ligne dont
// Aliment ressort vide. Un motif aliment_nu ne suffit pas — « sel » est une
// ligne parfaitement lue, sans quantité ni unité.
func TestLeCompteurCompteLesLignesNonLues(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)
	recette := recetteNeuve(t, app)

	lot := []string{
		"500 g de beurre demi-sel", // lue
		"???",                      // non lue — aliment_nu
		"sel",                      // lue, sans quantité ni unité
		"2",                        // non lue — quantite_aliment
		"( )",                      // non lue — aliment_nu
	}
	for _, brut := range lot {
		ligne := ingredientNeuf(t, app, recette, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de %q : %v", brut, err)
		}
	}

	if n := a.nonLues(); n != 3 {
		t.Errorf("%d ligne(s) non lue(s), attendu 3", n)
	}

	motifs := a.motifsNonLus()
	attendus := map[string]int{"aliment_nu": 2, "quantite_aliment": 1}
	if len(motifs) != len(attendus) {
		t.Fatalf("répartition par motif = %v, attendu %v", motifs, attendus)
	}
	for motif, compte := range attendus {
		if motifs[motif] != compte {
			t.Errorf("motif %q compté %d fois, attendu %d", motif, motifs[motif], compte)
		}
	}
}

func TestLeCompteurIgnoreUneMiseAJourSansChangementDeRaw(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)
	recette := recetteNeuve(t, app)

	ligne := ingredientNeuf(t, app, recette, "???")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement : %v", err)
	}
	ligne.Set("food", "poivre")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("ré-enregistrement : %v", err)
	}

	if n := a.nonLues(); n != 1 {
		t.Errorf("%d ligne(s) non lue(s), attendu 1 — la ligne a été recomptée", n)
	}
}

// --- Échappement ---------------------------------------------------------

// TestUneLigneHostileRessortEchappee : une recette importée est du contenu
// étranger par nature. Rien n'est assaini à l'écriture — raw doit rester mot
// pour mot — donc c'est le rendu qui échappe, et ça se vérifie (DOD.md §3).
func TestUneLigneHostileRessortEchappee(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	recette := recetteNeuve(t, app)

	const hostile = `2 <script>alert(1)</script> de sucre`
	ligne := ingredientNeuf(t, app, recette, hostile)
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement : %v", err)
	}

	relue, err := app.FindRecordById("ingredients", ligne.Id)
	if err != nil {
		t.Fatalf("relecture : %v", err)
	}
	if brut := relue.GetString("raw"); brut != hostile {
		t.Fatalf("raw = %q, attendu %q", brut, hostile)
	}
	if food := relue.GetString("food"); !strings.Contains(food, "<script>") {
		t.Fatalf("food = %q : le cas ne teste plus rien s'il ne porte pas de balise", food)
	}

	rendu, err := template.NewRegistry().
		LoadString(`<li>{{.Raw}} — {{.Food}}</li>`).
		Render(struct{ Raw, Food string }{relue.GetString("raw"), relue.GetString("food")})
	if err != nil {
		t.Fatalf("rendu : %v", err)
	}
	if strings.Contains(rendu, "<script>") {
		t.Errorf("balise script non échappée :\n%s", rendu)
	}
	if !strings.Contains(rendu, "&lt;script&gt;") {
		t.Errorf("balise script absente du rendu échappé :\n%s", rendu)
	}
}
