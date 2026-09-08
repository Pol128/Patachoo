package migrations

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// migrationDeLIndex : le fichier à défaire pour vérifier la descente.
const migrationDeLIndex = "1788858000_recherche_fts5.go"

// Les déclencheurs qui tiennent l'index à jour. Il y en a un par chemin
// d'écriture qui peut faire diverger l'index de la base — et pas un de plus :
// la suppression d'un tag passe par un enregistrement des recettes qui le
// portaient, donc par le déclencheur de mise à jour des recettes.
var declencheursDeLIndex = []string{
	"recipes_fts_apres_insertion_recette",
	"recipes_fts_apres_maj_recette",
	"recipes_fts_apres_suppression_recette",
	"recipes_fts_apres_insertion_ingredient",
	"recipes_fts_apres_maj_ingredient",
	"recipes_fts_apres_suppression_ingredient",
	"recipes_fts_apres_maj_tag",
}

// --- La table et ses déclencheurs ------------------------------------------

func TestLIndexDeRechercheEtSesDeclencheursSontCrees(t *testing.T) {
	app := baseNeuve(t)

	if !objetExiste(t, app, "table", "recipes_fts") {
		t.Error("la table virtuelle recipes_fts est absente")
	}
	for _, declencheur := range declencheursDeLIndex {
		if !objetExiste(t, app, "trigger", declencheur) {
			t.Errorf("le déclencheur %s est absent", declencheur)
		}
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownRetireLIndexEtSesDeclencheurs(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDeLIndex)

	if objetExiste(t, app, "table", "recipes_fts") {
		t.Error("recipes_fts survit au down")
	}
	for _, declencheur := range declencheursDeLIndex {
		if objetExiste(t, app, "trigger", declencheur) {
			t.Errorf("le déclencheur %s survit au down", declencheur)
		}
	}
}

// --- La réindexation -------------------------------------------------------

// Les recettes déjà en base au moment de la migration doivent entrer dans
// l'index, sinon elles disparaissent de la recherche. Le test vide l'index et
// rappelle la fonction : c'est exactement ce que fait la migration sur une
// base installée.
func TestLaReindexationRemetLesTroisSourcesDansLIndex(t *testing.T) {
	app := baseNeuve(t)
	recetteIndexee(t, app, "Tarte aux pommes",
		[]string{"3 pommes", "1 pate brisee"}, []string{"dessert"})
	recetteIndexee(t, app, "Soupe de potiron", []string{"1 potiron"}, nil)

	videLIndex(t, app)
	if reste := lignesDeLIndex(t, app); reste != 0 {
		t.Fatalf("%d lignes après le vidage, attendu 0", reste)
	}

	if err := reindexeLaRecherche(app); err != nil {
		t.Fatalf("réindexation : %v", err)
	}

	exigeTrouve(t, app, "tarte", "Tarte aux pommes")
	exigeTrouve(t, app, "brisee", "Tarte aux pommes")
	exigeTrouve(t, app, "dessert", "Tarte aux pommes")
	exigeTrouve(t, app, "potiron", "Soupe de potiron")
	if lignes := lignesDeLIndex(t, app); lignes != 2 {
		t.Errorf("%d lignes dans l'index, attendu 2 — une par recette", lignes)
	}
}

// --- Les déclencheurs, un par un -------------------------------------------

func TestLIndexSuitLaCreationDUneRecette(t *testing.T) {
	app := baseNeuve(t)

	recetteIndexee(t, app, "Omelette", []string{"4 oeufs"}, []string{"rapide"})

	exigeTrouve(t, app, "omelette", "Omelette")
	exigeTrouve(t, app, "oeufs", "Omelette")
	exigeTrouve(t, app, "rapide", "Omelette")
}

func TestLIndexSuitLeRenommageDUneRecette(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteIndexee(t, app, "Tarte aux pommes", nil, nil)

	recette.Set("title", "Tarte aux poires")
	if err := app.Save(recette); err != nil {
		t.Fatalf("renommage : %v", err)
	}

	exigeTrouve(t, app, "poires", "Tarte aux poires")
	exigeNeTrouvePas(t, app, "pommes")
}

func TestLIndexSuitLAjoutDUneLigneDIngredient(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteIndexee(t, app, "Gateau", []string{"200 g de farine"}, nil)

	ajouteUneLigne(t, app, recette, "1 gousse de vanille")

	exigeTrouve(t, app, "vanille", "Gateau")
	exigeTrouve(t, app, "farine", "Gateau")
}

func TestLIndexSuitLaModificationDUneLigneDIngredient(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteIndexee(t, app, "Gateau", []string{"200 g de farine"}, nil)

	ligne := lignesDeLaRecette(t, app, recette)[0]
	ligne.Set("raw", "200 g de sucre")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("modification de la ligne : %v", err)
	}

	exigeTrouve(t, app, "sucre", "Gateau")
	exigeNeTrouvePas(t, app, "farine")
}

func TestLIndexSuitLeRetraitDUneLigneDIngredient(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteIndexee(t, app, "Gateau",
		[]string{"200 g de farine", "1 gousse de vanille"}, nil)

	for _, ligne := range lignesDeLaRecette(t, app, recette) {
		if ligne.GetString("raw") != "1 gousse de vanille" {
			continue
		}
		if err := app.Delete(ligne); err != nil {
			t.Fatalf("suppression de la ligne : %v", err)
		}
	}

	exigeTrouve(t, app, "farine", "Gateau")
	exigeNeTrouvePas(t, app, "vanille")
}

// La recette part, ses lignes d'ingrédients partent avec elle par la cascade :
// l'index ne doit rien garder d'aucune des deux.
func TestLIndexSuitLaSuppressionDUneRecette(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteIndexee(t, app, "Gateau", []string{"200 g de farine"}, []string{"dessert"})

	if err := app.Delete(recette); err != nil {
		t.Fatalf("suppression de la recette : %v", err)
	}

	if lignes := lignesDeLIndex(t, app); lignes != 0 {
		t.Errorf("%d lignes dans l'index après la suppression, attendu 0", lignes)
	}
	exigeNeTrouvePas(t, app, "gateau")
	exigeNeTrouvePas(t, app, "farine")
}

func TestLIndexSuitLeRenommageDUnTag(t *testing.T) {
	app := baseNeuve(t)
	recetteIndexee(t, app, "Omelette", nil, []string{"rapide"})

	tag, err := app.FindFirstRecordByData("tags", "name", "rapide")
	if err != nil {
		t.Fatalf("tag rapide : %v", err)
	}
	tag.Set("name", "express")
	if err := app.Save(tag); err != nil {
		t.Fatalf("renommage du tag : %v", err)
	}

	exigeTrouve(t, app, "express", "Omelette")
	exigeNeTrouvePas(t, app, "rapide")
}

// Supprimer un tag ne demande pas de déclencheur à lui : PocketBase retire son
// identifiant des recettes qui le portaient et les enregistre, ce qui passe par
// le déclencheur de mise à jour des recettes. C'est ce chemin-là que ce test
// vérifie, et il rougirait si ce déclencheur ne rafraîchissait pas les tags.
func TestLIndexSuitLaSuppressionDUnTag(t *testing.T) {
	app := baseNeuve(t)
	recetteIndexee(t, app, "Omelette", nil, []string{"rapide", "vegetarien"})

	tag, err := app.FindFirstRecordByData("tags", "name", "rapide")
	if err != nil {
		t.Fatalf("tag rapide : %v", err)
	}
	if err := app.Delete(tag); err != nil {
		t.Fatalf("suppression du tag : %v", err)
	}

	exigeNeTrouvePas(t, app, "rapide")
	exigeTrouve(t, app, "vegetarien", "Omelette")
}

// --- Montage ---------------------------------------------------------------

// recetteIndexee enregistre une recette, ses lignes d'ingrédients et ses tags,
// par app.Save : c'est le chemin qu'emprunte tout le produit, et donc celui
// que les déclencheurs doivent suivre.
func recetteIndexee(t *testing.T, app core.App, titre string, lignes []string, nomsDeTags []string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", titre)
	if len(nomsDeTags) > 0 {
		recette.Set("tags", idsDesTags(t, app, nomsDeTags))
	}
	if err := app.Save(recette); err != nil {
		t.Fatalf("recette %q : %v", titre, err)
	}

	for _, ligne := range lignes {
		ajouteUneLigne(t, app, recette, ligne)
	}
	return recette
}

func idsDesTags(t *testing.T, app core.App, noms []string) []string {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}

	ids := make([]string, 0, len(noms))
	for _, nom := range noms {
		tag := core.NewRecord(collection)
		tag.Set("name", nom)
		tag.Set("slug", nom)
		if err := app.Save(tag); err != nil {
			t.Fatalf("tag %q : %v", nom, err)
		}
		ids = append(ids, tag.Id)
	}
	return ids
}

func ajouteUneLigne(t *testing.T, app core.App, recette *core.Record, brute string) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatalf("collection ingredients : %v", err)
	}
	ligne := core.NewRecord(collection)
	ligne.Set("recipe", recette.Id)
	ligne.Set("raw", brute)
	if err := app.Save(ligne); err != nil {
		t.Fatalf("ligne %q : %v", brute, err)
	}
}

func lignesDeLaRecette(t *testing.T, app core.App, recette *core.Record) []*core.Record {
	t.Helper()

	lignes, err := app.FindAllRecords("ingredients", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		t.Fatalf("lecture des lignes : %v", err)
	}
	return lignes
}

// --- Lecture de l'index ----------------------------------------------------

// titresTrouves interroge l'index comme la page le fera : le mot est cité et
// suffixé de « * », donc cherché en préfixe.
func titresTrouves(t *testing.T, app core.App, mot string) []string {
	t.Helper()

	titres := []string{}
	err := app.DB().
		NewQuery("SELECT title FROM recipes_fts WHERE recipes_fts MATCH {:q}").
		Bind(dbx.Params{"q": `"` + mot + `"*`}).
		Column(&titres)
	if err != nil {
		t.Fatalf("recherche de %q dans l'index : %v", mot, err)
	}
	return titres
}

func exigeTrouve(t *testing.T, app core.App, mot string, titre string) {
	t.Helper()

	for _, trouve := range titresTrouves(t, app, mot) {
		if trouve == titre {
			return
		}
	}
	t.Errorf("%q ne ramène pas %q dans l'index", mot, titre)
}

func exigeNeTrouvePas(t *testing.T, app core.App, mot string) {
	t.Helper()

	if trouves := titresTrouves(t, app, mot); len(trouves) > 0 {
		t.Errorf("%q ramène encore %v dans l'index", mot, trouves)
	}
}

func lignesDeLIndex(t *testing.T, app core.App) int {
	t.Helper()

	var compte int
	if err := app.DB().NewQuery("SELECT count(*) FROM recipes_fts").Row(&compte); err != nil {
		t.Fatalf("comptage de l'index : %v", err)
	}
	return compte
}

func videLIndex(t *testing.T, app core.App) {
	t.Helper()

	if _, err := app.DB().NewQuery("DELETE FROM recipes_fts").Execute(); err != nil {
		t.Fatalf("vidage de l'index : %v", err)
	}
}

// objetExiste lit sqlite_master : c'est la seule source qui dise si la
// migration a réellement posé la table et ses déclencheurs.
func objetExiste(t *testing.T, app core.App, genre string, nom string) bool {
	t.Helper()

	var compte int
	err := app.DB().
		NewQuery("SELECT count(*) FROM sqlite_master WHERE type = {:genre} AND name = {:nom}").
		Bind(dbx.Params{"genre": genre, "nom": nom}).
		Row(&compte)
	if err != nil {
		t.Fatalf("lecture de sqlite_master : %v", err)
	}
	return compte > 0
}
