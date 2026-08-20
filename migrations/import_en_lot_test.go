package migrations

import (
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// Les noms de collections, de champs et de valeurs sont un contrat entre les
// quatre sous-tâches de PATA-27 : les suivantes les emploient tels quels. Le
// test les énumère plutôt que de les compter — en changer un ici doit rougir,
// pas passer inaperçu.
var champsDuLot = map[string]map[string]string{
	"imports": {
		"created_by": core.FieldTypeRelation,
		"tag":        core.FieldTypeRelation,
		"status":     core.FieldTypeSelect,
		"created":    core.FieldTypeAutodate,
		"updated":    core.FieldTypeAutodate,
	},
	"import_urls": {
		"batch":    core.FieldTypeRelation,
		"url":      core.FieldTypeText,
		"position": core.FieldTypeNumber,
		"status":   core.FieldTypeSelect,
		"cause":    core.FieldTypeText,
		"code":     core.FieldTypeNumber,
		"recipe":   core.FieldTypeRelation,
		"created":  core.FieldTypeAutodate,
		"updated":  core.FieldTypeAutodate,
	},
}

// Les deux jeux de statuts, dans l'ordre du contrat. La comparaison porte sur
// la liste entière : une valeur ajoutée en passant est une valeur que
// l'ouvrier de la sous-tâche 3 ne saura pas traiter.
//
// Réécrits ici plutôt que lus depuis la migration : un test qui compare une
// variable à elle-même passe quoi qu'on y mette.
var (
	statutsAttendusDUnLot  = []string{"en_cours", "termine"}
	statutsAttendusDUneURL = []string{"a_faire", "en_cours", "importee", "deja_presente", "echec"}
	migrationDuLot         = "1787329500_import_en_lot.go"
	collectionsDuLot       = []string{"import_urls", "imports"}
)

func TestLesCollectionsDuLotOntLeursChamps(t *testing.T) {
	app := baseNeuve(t)

	for nom, attendus := range champsDuLot {
		collection, err := app.FindCollectionByNameOrId(nom)
		if err != nil {
			t.Errorf("collection %s absente : %v", nom, err)
			continue
		}
		for champ, typeAttendu := range attendus {
			pose := collection.Fields.GetByName(champ)
			if pose == nil {
				t.Errorf("%s.%s absent", nom, champ)
				continue
			}
			if pose.Type() != typeAttendu {
				t.Errorf("%s.%s est un %s, attendu %s", nom, champ, pose.Type(), typeAttendu)
			}
		}
	}
}

func TestLesStatutsDuLotSontExactementCeuxDuContrat(t *testing.T) {
	app := baseNeuve(t)

	for nom, attendues := range map[string][]string{
		"imports":     statutsAttendusDUnLot,
		"import_urls": statutsAttendusDUneURL,
	} {
		collection, err := app.FindCollectionByNameOrId(nom)
		if err != nil {
			t.Errorf("collection %s absente : %v", nom, err)
			continue
		}
		champ, ok := collection.Fields.GetByName("status").(*core.SelectField)
		if !ok {
			t.Errorf("%s.status n'est pas un select", nom)
			continue
		}
		if !slices.Equal(champ.Values, attendues) {
			t.Errorf("%s.status accepte %v, attendu %v", nom, champ.Values, attendues)
		}
	}
}

// Un lot supprimé emporte ses lignes, comme une recette emporte ses
// ingrédients. Sans cascade, la base garde la liste des URLs soumises par un
// compte longtemps après que celui-ci a effacé son lot.
func TestSupprimerUnLotEmporteSesURLs(t *testing.T) {
	app := baseNeuve(t)
	lot := lotNeuf(t, app)

	if err := urlSoumise(t, app, lot, "https://exemple.test/une", 1); err != nil {
		t.Fatalf("première URL : %v", err)
	}
	if err := urlSoumise(t, app, lot, "https://exemple.test/deux", 2); err != nil {
		t.Fatalf("seconde URL : %v", err)
	}

	if err := app.Delete(lot); err != nil {
		t.Fatalf("suppression du lot : %v", err)
	}

	restantes, err := app.FindAllRecords("import_urls")
	if err != nil {
		t.Fatal(err)
	}
	if len(restantes) != 0 {
		t.Errorf("%d ligne(s) orpheline(s) après la suppression du lot", len(restantes))
	}
}

// Soumettre deux fois la même URL dans une même fournée, c'est la télécharger
// deux fois et créer deux recettes identiques.
func TestUneMemeURLNeSeSoumetPasDeuxFoisDansUnLot(t *testing.T) {
	app := baseNeuve(t)
	lot := lotNeuf(t, app)

	if err := urlSoumise(t, app, lot, "https://exemple.test/tarte", 1); err != nil {
		t.Fatalf("première soumission : %v", err)
	}
	if err := urlSoumise(t, app, lot, "https://exemple.test/tarte", 2); err == nil {
		t.Error("la même URL est entrée deux fois dans le même lot")
	}
}

// Le cas normal d'un réimport : la même adresse, une autre fournée. L'unicité
// porte sur le couple, pas sur l'URL seule.
func TestLaMemeURLPeutRevenirDansUnAutreLot(t *testing.T) {
	app := baseNeuve(t)

	if err := urlSoumise(t, app, lotNeuf(t, app), "https://exemple.test/tarte", 1); err != nil {
		t.Fatalf("premier lot : %v", err)
	}
	if err := urlSoumise(t, app, lotNeuf(t, app), "https://exemple.test/tarte", 1); err != nil {
		t.Errorf("second lot : %v — un réimport de la même URL doit rester permis", err)
	}
}

// Une ligne sans lot n'est rattachable à rien : ni reprise, ni rapport.
func TestUneURLDeLotSansLotEstRefusee(t *testing.T) {
	app := baseNeuve(t)

	if err := urlSoumise(t, app, nil, "https://exemple.test/orpheline", 1); err == nil {
		t.Error("une ligne import_urls sans batch a été enregistrée")
	}
}

// L'ouvrier réclame en boucle la prochaine ligne « a_faire » d'un lot : c'est
// la seule lecture chaude du schéma, et la seule que rien d'autre ne couvre —
// l'index unique, lui, se voit dans le comportement.
func TestLIndexQueLitLOuvrierExiste(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("import_urls")
	if err != nil {
		t.Fatal(err)
	}
	index := collection.GetIndex("idx_import_urls_batch_status")
	if index == "" {
		t.Fatal("idx_import_urls_batch_status absent : la boucle de l'ouvrier balaiera la table")
	}
	for _, colonne := range []string{"batch", "status"} {
		if !strings.Contains(index, colonne) {
			t.Errorf("idx_import_urls_batch_status ne porte pas %s : %s", colonne, index)
		}
	}
}

// Le sens du refus (DOD.md §3). Patachoo lit et écrit cet état depuis son
// propre code Go, qui ne passe pas par les règles : les ouvrir n'apporterait
// rien et exposerait la liste des URLs soumises par chaque compte. Ce test
// rougira le jour où quelqu'un le fera sans le décider.
func TestLesCollectionsDuLotRestentFermeesALAPI(t *testing.T) {
	app := baseNeuve(t)

	for _, nom := range collectionsDuLot {
		for verbe, regle := range reglesDe(t, app, nom) {
			if regle != nil {
				t.Errorf("%s.%sRule = %q, attendu nil : la collection doit rester réservée au superuser",
					nom, verbe, *regle)
			}
		}
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownRetireLesCollectionsDuLot(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDuLot)

	for _, nom := range collectionsDuLot {
		if _, err := app.FindCollectionByNameOrId(nom); err == nil {
			t.Errorf("collection %s toujours présente après le down", nom)
		}
	}
}

// lotNeuf enregistre un lot en cours, sans compte ni tag : ni l'un ni l'autre
// n'est obligatoire, et la sous-tâche 2 les posera.
func lotNeuf(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("imports")
	if err != nil {
		t.Fatalf("collection imports : %v", err)
	}
	lot := core.NewRecord(collection)
	lot.Set("status", "en_cours")
	if err := app.Save(lot); err != nil {
		t.Fatalf("enregistrement du lot : %v", err)
	}
	return lot
}

// urlSoumise enregistre une URL dans un lot et rend l'erreur telle quelle :
// les cas d'échec sont la moitié de ce que cette collection doit garantir. Un
// lot nil laisse batch vide, ce qui est justement le cas à refuser.
func urlSoumise(t *testing.T, app core.App, lot *core.Record, adresse string, position int) error {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("import_urls")
	if err != nil {
		t.Fatalf("collection import_urls : %v", err)
	}
	ligne := core.NewRecord(collection)
	if lot != nil {
		ligne.Set("batch", lot.Id)
	}
	ligne.Set("url", adresse)
	ligne.Set("position", position)
	ligne.Set("status", "a_faire")
	return app.Save(ligne)
}
