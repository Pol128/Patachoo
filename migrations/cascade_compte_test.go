package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// migrationDeLaCascade : le fichier à défaire pour vérifier la descente.
const migrationDeLaCascade = "1788965651_cascade_compte_supprime.go"

// --- Le drapeau, sur les deux relations -------------------------------------

// Les trois relations vers users se comportent enfin pareil : comments.author
// est en cascade depuis PATA-38, created_by ne l'était nulle part. Sans elle,
// supprimer un compte laisse une relation qui ne désigne plus rien, et
// PocketBase refuse ensuite d'enregistrer l'enregistrement concerné.
func TestLesRecettesSuiventLeCompteSupprime(t *testing.T) {
	app := baseNeuve(t)

	if champ := relationDe(t, app, "recipes", "created_by"); !champ.CascadeDelete {
		t.Error("recipes.created_by sans cascadeDelete")
	}
}

func TestLesLotsSuiventLeCompteSupprime(t *testing.T) {
	app := baseNeuve(t)

	if champ := relationDe(t, app, "imports", "created_by"); !champ.CascadeDelete {
		t.Error("imports.created_by sans cascadeDelete")
	}
}

// La cascade ne doit pas emporter au passage la seule autre propriété du
// champ. created_by reste facultatif sur recipes, pour la raison écrite dans
// le schéma initial : il est rempli par le code après coup, et une recette
// peut légitimement le porter vide — c'est le cas de celles saisies avant
// PATA-35, et celui que le premier terme de la règle de suppression protège.
func TestLAuteurDUneRecetteResteFacultatif(t *testing.T) {
	app := baseNeuve(t)

	if relationDe(t, app, "recipes", "created_by").Required {
		t.Error("recipes.created_by est devenu obligatoire : les recettes sans auteur " +
			"ne seraient plus enregistrables")
	}
}

// --- La chaîne, jusqu'au bout ----------------------------------------------

// PocketBase applique ses cascades enregistrement par enregistrement, en Go,
// et non par une contrainte SQL : que la recette emporte ses ingrédients et
// ses notes quand c'est la suppression du compte qui l'emporte elle-même ne se
// déduit pas des drapeaux, ça se vérifie.
func TestSupprimerUnCompteEmporteSaRecetteSesIngredientsEtSesNotes(t *testing.T) {
	app := baseNeuve(t)
	titulaire := compteNeuf(t, app, "titulaire@exemple.test")
	recette := recetteDe(t, app, titulaire)
	ajouteUneLigne(t, app, recette, "200 g de farine")
	ligne := lignesDeLaRecette(t, app, recette)[0]
	note := noteDe(t, app, recette, titulaire, "Cuite dix minutes de plus.")

	if err := app.Delete(titulaire); err != nil {
		t.Fatalf("suppression du compte : %v", err)
	}

	exigeDisparu(t, app, "users", titulaire.Id, "le compte")
	exigeDisparu(t, app, "recipes", recette.Id, "la recette du compte")
	exigeDisparu(t, app, "ingredients", ligne.Id, "la ligne d'ingrédient de la recette")
	exigeDisparu(t, app, "comments", note.Id, "la note sous la recette")
}

// Le lot emporte ses lignes par la cascade posée sur import_urls.batch : la
// chaîne complète part donc du compte.
func TestSupprimerUnCompteEmporteSesLotsEtLeursLignes(t *testing.T) {
	app := baseNeuve(t)
	titulaire := compteNeuf(t, app, "titulaire@exemple.test")
	lot := lotDe(t, app, titulaire)
	if err := urlSoumise(t, app, lot, "https://exemple.test/une", 1); err != nil {
		t.Fatalf("URL du lot : %v", err)
	}

	if err := app.Delete(titulaire); err != nil {
		t.Fatalf("suppression du compte : %v", err)
	}

	exigeDisparu(t, app, "imports", lot.Id, "le lot lancé par le compte")
	if restantes, err := app.FindAllRecords("import_urls"); err != nil {
		t.Fatal(err)
	} else if len(restantes) != 0 {
		t.Errorf("%d ligne(s) de lot orpheline(s) après la suppression du compte", len(restantes))
	}
}

// Sans ce test, une cascade posée sur la mauvaise relation passerait au vert :
// tout emporter est aussi faux que ne rien emporter.
func TestLeContenuDUnAutreCompteNeBougePas(t *testing.T) {
	app := baseNeuve(t)
	partant := compteNeuf(t, app, "partant@exemple.test")
	restant := compteNeuf(t, app, "restant@exemple.test")
	recetteDe(t, app, partant)
	lotDe(t, app, partant)
	saRecette := recetteDe(t, app, restant)
	sonLot := lotDe(t, app, restant)

	if err := app.Delete(partant); err != nil {
		t.Fatalf("suppression du compte partant : %v", err)
	}

	exigePresent(t, app, "users", restant.Id, "le compte restant")
	exigePresent(t, app, "recipes", saRecette.Id, "la recette du compte restant")
	exigePresent(t, app, "imports", sonLot.Id, "le lot du compte restant")
}

// Une recette sans auteur n'est référencée par aucun compte : elle ne peut
// suivre aucune suppression. C'est le cas des recettes saisies avant PATA-35.
func TestUneRecetteSansAuteurSurvitALaSuppressionDUnCompte(t *testing.T) {
	app := baseNeuve(t)
	orpheline := recetteDe(t, app, nil)
	titulaire := compteNeuf(t, app, "titulaire@exemple.test")

	if err := app.Delete(titulaire); err != nil {
		t.Fatalf("suppression du compte : %v", err)
	}

	exigePresent(t, app, "recipes", orpheline.Id, "la recette sans auteur")
}

// Le déclencheur recipes_fts_apres_suppression_recette existe depuis PATA-33.
// Qu'une suppression en cascade le déclenche, elle aussi, ne se déduit pas de
// sa présence : sans ça, l'index garderait des recettes que la recherche
// ramènerait vers une page introuvable.
func TestLIndexDeRechercheSuitLesRecettesEmportees(t *testing.T) {
	app := baseNeuve(t)
	titulaire := compteNeuf(t, app, "titulaire@exemple.test")
	recette := recetteDe(t, app, titulaire)
	ajouteUneLigne(t, app, recette, "200 g de farine")

	if err := app.Delete(titulaire); err != nil {
		t.Fatalf("suppression du compte : %v", err)
	}

	if lignes := lignesDeLIndex(t, app); lignes != 0 {
		t.Errorf("%d ligne(s) dans recipes_fts après la suppression du compte, attendu 0", lignes)
	}
	exigeNeTrouvePas(t, app, "tarte")
	exigeNeTrouvePas(t, app, "farine")
}

// --- Le retour en arrière ---------------------------------------------------

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownRendLesRelationsNonCascadantes(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDeLaCascade)

	for _, collection := range []string{"recipes", "imports"} {
		if relationDe(t, app, collection, "created_by").CascadeDelete {
			t.Errorf("%s.created_by est encore en cascade après le down", collection)
		}
	}
}

// --- Fabriques et lectures --------------------------------------------------

// relationDe rend un champ de relation, ou fait échouer le test : le drapeau
// de cascade ne vit que sur le type concret.
func relationDe(t *testing.T, app core.App, collection, nom string) *core.RelationField {
	t.Helper()

	trouvee, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("collection %s : %v", collection, err)
	}
	champ, ok := trouvee.Fields.GetByName(nom).(*core.RelationField)
	if !ok {
		t.Fatalf("%s.%s n'est pas une relation", collection, nom)
	}
	return champ
}

// lotDe enregistre un lot lancé par le compte donné. lotNeuf, lui, en laisse
// le titulaire vide — c'est justement ce que la cascade ne doit pas emporter.
func lotDe(t *testing.T, app core.App, titulaire *core.Record) *core.Record {
	t.Helper()

	lot := lotNeuf(t, app)
	lot.Set("created_by", titulaire.Id)
	if err := app.Save(lot); err != nil {
		t.Fatalf("attribution du lot à %s : %v", titulaire.Id, err)
	}
	return lot
}

// exigeDisparu lit l'enregistrement et exige l'erreur : c'est la seule lecture
// qui distingue un enregistrement supprimé d'un enregistrement resté en base
// avec une relation pendante.
func exigeDisparu(t *testing.T, app core.App, collection, id, quoi string) {
	t.Helper()

	if _, err := app.FindRecordById(collection, id); err == nil {
		t.Errorf("%s survit à la suppression du compte", quoi)
	}
}

func exigePresent(t *testing.T, app core.App, collection, id, quoi string) {
	t.Helper()

	if _, err := app.FindRecordById(collection, id); err != nil {
		t.Errorf("%s a disparu : %v", quoi, err)
	}
}
