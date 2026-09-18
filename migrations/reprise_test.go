package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// fichierDeLaReprise sert au retour en arrière : defaitJusqua désigne une
// migration par son nom de fichier.
const fichierDeLaReprise = "1789725600_reprise_des_echecs.go"

// Le rapport d'une fournée devient une liste de choses à faire : chaque adresse
// en échec porte une case que l'utilisateur coche lui-même, et qu'il retrouve
// cochée en revenant sur la page.
//
// Un booléen, et non une date de reprise : l'énoncé demande de cocher et de
// retrouver coché, et horodater serait une donnée que rien n'affiche.
func TestLaRepriseEstUnBooleenSurImportUrls(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("import_urls")
	if err != nil {
		t.Fatalf("collection import_urls : %v", err)
	}

	pose := collection.Fields.GetByName("handled")
	if pose == nil {
		t.Fatalf("import_urls.handled absent")
	}
	if pose.Type() != core.FieldTypeBool {
		t.Errorf("import_urls.handled est un %s, attendu %s", pose.Type(), core.FieldTypeBool)
	}
}

// Faux par défaut : les lignes déjà en base restent non cochées. Une migration
// qui les cocherait ferait dire au rapport d'une fournée ancienne que tout a
// été repris, ce que personne n'a jamais fait.
func TestUneLigneNeuveNEstPasReprise(t *testing.T) {
	app := baseNeuve(t)
	ligne := ligneDeLotEnBase(t, app)

	if ligne.GetBool("handled") {
		t.Errorf("une ligne neuve est déjà reprise")
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownDeLaRepriseRetireLeChamp(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaReprise)

	collection, err := app.FindCollectionByNameOrId("import_urls")
	if err != nil {
		t.Fatalf("collection import_urls : %v", err)
	}
	if pose := collection.Fields.GetByName("handled"); pose != nil {
		t.Errorf("import_urls.handled est encore là après le down")
	}
}

// ligneDeLotEnBase écrit un lot et l'une de ses lignes, au plus court : ce
// test-ci ne porte que sur le champ ajouté, pas sur ce qui l'entoure.
func ligneDeLotEnBase(t *testing.T, app core.App) *core.Record {
	t.Helper()

	lots, err := app.FindCollectionByNameOrId("imports")
	if err != nil {
		t.Fatalf("collection imports : %v", err)
	}
	lot := core.NewRecord(lots)
	lot.Set("status", "en_cours")
	if err := app.Save(lot); err != nil {
		t.Fatalf("création du lot : %v", err)
	}

	lignes, err := app.FindCollectionByNameOrId("import_urls")
	if err != nil {
		t.Fatalf("collection import_urls : %v", err)
	}
	ligne := core.NewRecord(lignes)
	ligne.Set("batch", lot.Id)
	ligne.Set("url", "https://exemple.fr/une")
	ligne.Set("position", 1)
	ligne.Set("status", "a_faire")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("création de la ligne : %v", err)
	}
	return ligne
}
