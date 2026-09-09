package migrations

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// baseNeuve monte une base vide et lui applique les migrations, exactement
// comme le ferait `go run . migrate up` sur une installation fraîche.
func baseNeuve(t *testing.T) core.App {
	t.Helper()

	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("amorçage : %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrations : %v", err)
	}
	return app
}

// TestBaseNeuveNArrivePasAvecUneGoroutineDeJournal est le pendant, pour ce
// montage-ci, du test de même nom du paquet racine — qui porte l'explication
// complète du mécanisme. Résumé : PocketBase lance à l'amorçage une goroutine
// qui purge le journal toutes les trois secondes et lit IsBootstrapped() sans
// verrou, quand ResetBootstrapState() écrit dans les mêmes champs sans verrou
// non plus. Seul le crochet OnTerminate arrête ce minuteur.
//
// Le décompte est recopié plutôt que partagé : Go n'exporte pas les fonctions
// d'un fichier _test.go d'un paquet à l'autre, et une correction non gardée
// par un test se ferait retirer sans que rien ne rougisse.
func TestBaseNeuveNArrivePasAvecUneGoroutineDeJournal(t *testing.T) {
	t.Run("une base montée puis rendue", func(t *testing.T) {
		baseNeuve(t)
	})

	echeance := time.Now().Add(5 * time.Second)
	for {
		nombre, piles := goroutinesDuJournal()
		if nombre == 0 {
			return
		}
		if time.Now().After(echeance) {
			t.Fatalf("%d goroutine(s) de journal PocketBase survivent au montage : "+
				"le minuteur n'a pas été arrêté avant ResetBootstrapState\n\n%s", nombre, piles)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// goroutinesDuJournal compte les goroutines du minuteur de journal de
// PocketBase encore vivantes, et rend leurs piles pour le message d'échec.
func goroutinesDuJournal() (int, string) {
	piles := make([]byte, 1<<16)
	for {
		n := runtime.Stack(piles, true)
		if n < len(piles) {
			piles = piles[:n]
			break
		}
		piles = make([]byte, 2*len(piles))
	}

	var vivantes []string
	// runtime.Stack sépare les goroutines par une ligne vide.
	for _, pile := range strings.Split(string(piles), "\n\n") {
		if strings.Contains(pile, "core.(*BaseApp).initLogger") {
			vivantes = append(vivantes, pile)
		}
	}
	return len(vivantes), strings.Join(vivantes, "\n\n")
}

func TestLesCollectionsSontCreees(t *testing.T) {
	app := baseNeuve(t)

	for _, nom := range []string{"recipes", "ingredients", "tags", "meal_types"} {
		if _, err := app.FindCollectionByNameOrId(nom); err != nil {
			t.Errorf("collection %s absente : %v", nom, err)
		}
	}
}

// Le critère de la tâche : une base fraîche doit être utilisable sans
// configuration. Une liste de types de plat vide obligerait chacun à inventer
// la sienne avant d'enregistrer sa première recette.
func TestUneBaseFraicheASesTypesDePlat(t *testing.T) {
	app := baseNeuve(t)

	types, err := app.FindAllRecords("meal_types")
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != len(typesDePlatInitiaux) {
		t.Fatalf("%d types de plat, attendu %d", len(types), len(typesDePlatInitiaux))
	}
	for _, enregistrement := range types {
		if enregistrement.GetString("slug") == "" {
			t.Errorf("%q sans slug", enregistrement.GetString("name"))
		}
		if enregistrement.GetInt("position") == 0 {
			t.Errorf("%q sans position : la barre de filtres serait triée au hasard",
				enregistrement.GetString("name"))
		}
	}
}

// raw est le champ le plus important du schéma : c'est lui qui garde une
// recette lisible quand le parser échoue. Il doit être obligatoire, et rester
// le seul de sa collection à l'être avec la recette qui le porte.
func TestLaLigneBruteEstObligatoire(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatal(err)
	}

	obligatoires := map[string]bool{}
	for _, champ := range collection.Fields {
		if estObligatoire(champ) {
			obligatoires[champ.GetName()] = true
		}
	}

	if !obligatoires["raw"] {
		t.Error("raw n'est pas obligatoire : une ligne pourrait exister sans son texte d'origine")
	}
	for _, sortieDuParser := range []string{"quantity", "unit", "food", "note"} {
		if obligatoires[sortieDuParser] {
			t.Errorf("%s est obligatoire : le parser rend parfois vide, la ligne doit passer quand même",
				sortieDuParser)
		}
	}
}

// Supprimer une recette doit emporter ses ingrédients, sinon la base accumule
// des lignes orphelines que plus rien ne référence.
func TestLesIngredientsSuiventLaRecetteSupprimee(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatal(err)
	}
	champ, ok := collection.Fields.GetByName("recipe").(*core.RelationField)
	if !ok {
		t.Fatal("recipe n'est pas une relation")
	}
	if !champ.CascadeDelete {
		t.Error("recipe sans cascadeDelete")
	}
}

// estObligatoire lit le drapeau Required, que l'interface core.Field n'expose
// pas : il vit sur chaque type concret.
func estObligatoire(champ core.Field) bool {
	switch f := champ.(type) {
	case *core.TextField:
		return f.Required
	case *core.NumberField:
		return f.Required
	case *core.BoolField:
		return f.Required
	case *core.RelationField:
		return f.Required
	case *core.SelectField:
		return f.Required
	case *core.URLField:
		return f.Required
	case *core.EditorField:
		return f.Required
	case *core.FileField:
		return f.Required
	default:
		return false
	}
}

// defaitJusqua rejoue vers le bas jusqu'à défaire la migration du fichier
// donné, celle-ci comprise.
//
// Le nombre de migrations à défaire se calcule, il ne se code pas : un
// « Down(1) » écrit quand la migration visée était la dernière défait la
// suivante à sa place le jour où on en ajoute une, et le test rougit sans que
// rien ne soit cassé.
func defaitJusqua(t *testing.T, app core.App, fichier string) {
	t.Helper()

	liste := core.MigrationsList{}
	liste.Copy(core.SystemMigrations)
	liste.Copy(core.AppMigrations)

	// La liste est triée par nom de fichier, comme l'ordre d'application :
	// tout ce qui suit la migration visée doit être défait avec elle.
	rang := slices.IndexFunc(liste.Items(), func(migration *core.Migration) bool {
		return migration.File == fichier
	})
	if rang < 0 {
		t.Fatalf("migration %s introuvable", fichier)
	}

	if _, err := core.NewMigrationsRunner(app, liste).Down(len(liste.Items()) - rang); err != nil {
		t.Fatalf("retour en arrière : %v", err)
	}
}
