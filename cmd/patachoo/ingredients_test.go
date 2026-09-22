package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/template"
	"golang.org/x/crypto/bcrypt"
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

// baseNeuveAvec monte une base neuve, y branche nos hooks avec l'analyseur
// donné, puis applique les migrations — dans l'ordre où main() le fait.
func baseNeuveAvec(t *testing.T, a *analyseur) core.App {
	t.Helper()
	return baseNeuveAuCout(t, a, coutBcryptDesTests)
}

// Le coût bcrypt des comptes de test.
//
// Un test qui ouvre une session paie deux bcrypt complets : hacher le mot de
// passe à la création du compte, puis le vérifier à la connexion. Au facteur
// par défaut de PocketBase, c'était la moitié du coût d'un test.
//
// Le facteur est un réglage du champ, et la vérification lit le sien dans le
// hash : l'abaisser dans la fixture rend les deux opérations quasi gratuites.
// Le mot de passe reste haché et vérifié pour de vrai — c'est le nombre de
// tours qui baisse, pas le mécanisme —, et la production ne bouge pas : elle
// garde le défaut de PocketBase, que ce fichier ne touche jamais.
//
// Un test qui a besoin du coût réel prend `baseNeuveAuCoutBcryptReel`, ou
// `serveurDeTestAuCoutBcryptReel`. Le seul du paquet est celui qui mesure un
// écart de temps, et son garde-fou refuse de conclure sur un bcrypt bradé.
const coutBcryptDesTests = bcrypt.MinCost

// baseNeuveAuCoutBcryptReel laisse à PocketBase son facteur par défaut, au prix
// d'environ 200 ms par compte créé puis connecté. Elle ne passe pas par le
// gabarit, qui porte le facteur abaissé : sa base est migrée pour elle seule.
func baseNeuveAuCoutBcryptReel(t *testing.T, a *analyseur) core.App {
	t.Helper()
	return baseNeuveAuCout(t, a, 0)
}

// baseNeuveAuCout monte la base. `cout` à zéro laisse le facteur bcrypt de
// PocketBase. Le nom dit « au coût » parce que `baseNeuve` est déjà pris
// (tags_test.go) et désigne autre chose.
//
// Au coût des tests, la base n'est pas migrée mais recopiée du gabarit, et
// c'est ce qui fait la durée du paquet. Relevé le 22/09/2026 (PATA-130), le coût
// d'un test est son montage, et le montage est d'abord la migration : 279 ms en
// passe courte, 2,5 s sous -race — plus de la moitié du montage, alors que le
// corps d'un test pèse moins de 10 % du tout. Recopier le gabarit coûte moins
// d'une milliseconde, sous -race comme sans.
//
// L'isolation n'y perd rien : chaque test a sa copie dans son propre
// répertoire, son BaseApp et son nettoyage. Ce qui est mis en commun, c'est le
// travail de migration, pas la base. Mais ce que les migrations tirent au
// hasard est tiré une fois par passe au lieu d'une fois par test : les secrets
// de jeton des collections d'authentification, les identifiants des types de
// plat et celui des réglages. Aucun test ne fait voyager une de ces valeurs
// d'une base à l'autre, ni ne compte sur leur nouveauté — vérifié le 23/09/2026
// sur tout le paquet (PATA-129). Un test qui en aurait besoin prend
// `baseNeuveAuCoutBcryptReel`, migrée pour lui seul.
func baseNeuveAuCout(t *testing.T, a *analyseur, cout int) core.App {
	t.Helper()

	dir := t.TempDir()
	recopiee := cout == coutBcryptDesTests
	if recopiee {
		recopieLeGabarit(t, dir)
	}

	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
	// Terminer avant de réinitialiser, et non l'inverse : c'est OnTerminate qui
	// arrête le minuteur de purge du journal, et le commentaire de PocketBase
	// au-dessus du crochet dit pourquoi l'ordre compte — « to avoid races with
	// ResetBootstrap user calls ». Ce nettoyage-ci est posé le premier, donc
	// dépilé le dernier : il passe après ceux du test. Déclencher OnTerminate
	// une fois de plus est sans effet.
	t.Cleanup(func() {
		_ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app})
		_ = app.ResetBootstrapState()
	})

	brancheLesHooks(app, a)

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("amorçage : %v", err)
	}
	// Sur une copie, les migrations sont déjà jouées et celle-ci ne fait que
	// le constater : une milliseconde en passe courte, quelques dizaines sous
	// -race. Elle reste, parce qu'elle rattrape un gabarit auquel il en
	// manquerait une.
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("migrations : %v", err)
	}
	if cout > 0 && !recopiee {
		if err := abaisseLeCoutBcrypt(app, cout); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

// Le gabarit : une base migrée une fois par passe, que chaque test recopie.
//
// Construit à la demande, au premier test qui monte une base, et jamais avant :
// le binaire relancé en sous-processus par sante_test.go n'en construit donc
// aucun. Jamais gardé sur disque d'une passe à l'autre non plus — une base
// migrée par une version antérieure des migrations se ferait passer pour
// neuve. C'est TestMain qui le retire, les tests joués.
var (
	gabaritUneFois sync.Once
	gabaritDir     string
	gabaritErr     error
)

// recopieLeGabarit recopie le gabarit dans dst, en le construisant s'il
// n'existe pas encore.
func recopieLeGabarit(t *testing.T, dst string) {
	t.Helper()

	gabaritUneFois.Do(func() {
		gabaritDir, gabaritErr = construitLeGabarit()
		if gabaritErr == nil {
			t.Logf("gabarit de base construit dans %s", gabaritDir)
		}
	})
	if gabaritErr != nil {
		t.Fatalf("gabarit de base : %v", gabaritErr)
	}
	if err := os.CopyFS(dst, os.DirFS(gabaritDir)); err != nil {
		t.Fatalf("recopie du gabarit : %v", err)
	}
}

// construitLeGabarit monte une base comme baseNeuveAuCout le faisait pour
// chaque test — hooks, amorçage, migrations, facteur bcrypt abaissé —, puis la
// ferme. Le facteur voyage avec la copie : le reposer après coûterait un
// enregistrement de collection par test, plus d'un tiers de seconde sous -race.
//
// Elle rend une erreur plutôt que d'échouer le test : elle tourne sous un
// sync.Once, et un t.Fatal y marquerait le gabarit construit sans qu'il le soit.
func construitLeGabarit() (dir string, err error) {
	dir, err = os.MkdirTemp("", "patachoo-gabarit-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	a, err := analyseurFR()
	if err != nil {
		return "", fmt.Errorf("chargement du pack français : %w", err)
	}
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: dir})
	defer func() {
		_ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app})
		_ = app.ResetBootstrapState()
	}()

	brancheLesHooks(app, a)
	if err := app.Bootstrap(); err != nil {
		return "", fmt.Errorf("amorçage : %w", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		return "", fmt.Errorf("migrations : %w", err)
	}
	if err := abaisseLeCoutBcrypt(app, coutBcryptDesTests); err != nil {
		return "", err
	}
	return dir, nil
}

// retireLeGabarit efface le gabarit de la passe, s'il a été construit.
func retireLeGabarit() {
	if gabaritDir != "" {
		_ = os.RemoveAll(gabaritDir)
	}
}

// abaisseLeCoutBcrypt repose le facteur du champ mot de passe des deux
// collections d'authentification. Après les migrations : ce sont elles qui
// créent les collections.
func abaisseLeCoutBcrypt(app core.App, cout int) error {
	for _, nom := range []string{"users", core.CollectionNameSuperusers} {
		collection, err := app.FindCollectionByNameOrId(nom)
		if err != nil {
			return fmt.Errorf("collection %s : %w", nom, err)
		}
		champ, ok := collection.Fields.GetByName(core.FieldNamePassword).(*core.PasswordField)
		if !ok {
			return fmt.Errorf("collection %s : champ %q absent ou d'un autre type",
				nom, core.FieldNamePassword)
		}
		champ.Cost = cout
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("collection %s : %w", nom, err)
		}
	}
	return nil
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

// relit relit la ligne depuis la base.
//
// Toute mise à jour passe par là, et c'est le chemin réel : l'API REST, le
// formulaire et l'administration relisent l'enregistrement avant de le
// modifier. C'est aussi ce qui peuple Original(), sur quoi le hook s'appuie
// pour savoir si raw a bougé.
func relit(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()

	ligne, err := app.FindRecordById("ingredients", id)
	if err != nil {
		t.Fatalf("relecture de %s : %v", id, err)
	}
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

// --- Le montage lui-même --------------------------------------------------

// TestBaseNeuveAvecNArrivePasAvecUneGoroutineDeJournal ferme la porte par
// laquelle la passe -race est rouge par intermittence.
//
// PocketBase lance à l'amorçage une goroutine qui purge le journal toutes les
// trois secondes, et lit pour cela IsBootstrapped() — qui déréférence
// concurrentDB et auxConcurrentDB sans verrou. ResetBootstrapState() écrit nil
// dans ces mêmes champs, sans verrou non plus. Le seul arrêt prévu est le
// crochet OnTerminate, et le commentaire de PocketBase juste au-dessus nomme
// le piège : « write all remaining logs before ticker.Stop to avoid races with
// ResetBootstrap user calls ».
//
// Le paquet racine monte une base par test : un montage qui abandonne son
// minuteur laisse derrière lui, jusqu'à la fin du binaire de test, une
// goroutine qui tire au sort toutes les trois secondes contre le nettoyage des
// tests suivants. C'est un rouge qui ne parle de rien, donc un rouge qu'on
// apprend à ignorer.
//
// Le constat se fait sur les piles et avec une échéance, jamais sur une
// lecture unique : le crochet arrête le minuteur et signale done, mais la
// goroutine ne sort qu'à son prochain passage dans son select.
func TestBaseNeuveAvecNArrivePasAvecUneGoroutineDeJournal(t *testing.T) {
	// Un sous-test, pour que ses nettoyages soient dépilés — celui de
	// baseNeuveAvec compris — pendant que le test parent, lui, vit encore.
	t.Run("une base montée puis rendue", func(t *testing.T) {
		baseNeuveAvec(t, analyseurDeTest(t))
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
//
// Le décompte est absolu et non un delta : le paquet ne monte de base que par
// baseNeuveAvec, donc une seule survivante suffit à dire que le montage fuit —
// qu'elle vienne de ce test-ci ou d'un précédent.
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
			verifieLesCinqChamps(t, champsLus(a.lit(c.brut)), champsIngredient{
				quantite:  c.quantite,
				unite:     c.unite,
				aliment:   c.aliment,
				note:      c.note,
				optionnel: c.optionnel,
			})
		})
	}
}

// verifieLesCinqChamps compare une lecture aux cinq champs attendus, et les
// signale tous les cinq plutôt que de s'arrêter au premier : un motif qui
// change en fait bouger plusieurs à la fois, et les voir ensemble dit lequel
// des deux a régressé.
func verifieLesCinqChamps(t *testing.T, obtenus, attendus champsIngredient) {
	t.Helper()

	switch {
	case attendus.quantite == nil && obtenus.quantite != nil:
		t.Errorf("quantity = %v, attendu vide", *obtenus.quantite)
	case attendus.quantite != nil && obtenus.quantite == nil:
		t.Errorf("quantity vide, attendu %v", *attendus.quantite)
	case attendus.quantite != nil && *obtenus.quantite != *attendus.quantite:
		t.Errorf("quantity = %v, attendu %v", *obtenus.quantite, *attendus.quantite)
	}
	if obtenus.unite != attendus.unite {
		t.Errorf("unit = %q, attendu %q", obtenus.unite, attendus.unite)
	}
	if obtenus.aliment != attendus.aliment {
		t.Errorf("food = %q, attendu %q", obtenus.aliment, attendus.aliment)
	}
	if obtenus.note != attendus.note {
		t.Errorf("note = %q, attendu %q", obtenus.note, attendus.note)
	}
	if obtenus.optionnel != attendus.optionnel {
		t.Errorf("optional = %v, attendu %v", obtenus.optionnel, attendus.optionnel)
	}
}

// TestLesQuatreMotifsCorrigesArriventJusquAuxCinqChamps prouve que les quatre
// correctifs du pack de langue traversent Patachoo, et pas seulement les tests
// du module qui les porte.
//
// Une montée de version est un changement que rien ne vérifie tant qu'il n'a
// pas de test de ce côté-ci : le pack et le lexique sont embarqués dans
// moteur, et ce qu'ils lisent n'arrive à la base qu'à travers champsLus.
// D'où le chemin réel — analyseurFR() puis champsLus(a.lit(brut)) —, et les
// cinq champs vérifiés à chaque fois plutôt que le seul food.
//
// Les quatre lignes viennent du corpus de l'instance, pas d'un exemple
// inventé : ce sont elles qui ont motivé les quatre sous-tâches de PATA-108.
func TestLesQuatreMotifsCorrigesArriventJusquAuxCinqChamps(t *testing.T) {
	a := analyseurDeTest(t)

	nombre := func(v float64) *float64 { return &v }

	cas := []struct {
		motif    string
		brut     string
		attendus champsIngredient
	}{
		// La préparation qui suit la virgule terminale part en note, au lieu
		// de rester collée à l'aliment.
		{
			motif: "préparation après une virgule finale",
			brut:  "2 oignons, hachés finement",
			attendus: champsIngredient{
				quantite: nombre(2), aliment: "oignons", note: "hachés finement",
			},
		},
		// Le motif inversé « Aliment : quantité » se lisait entièrement de
		// travers : la ligne entière restait dans food, sans quantité ni
		// unité. L'aliment ressort au singulier parce que le lexique rend
		// désormais la forme canonique de l'entrée qu'il a reconnue.
		{
			motif: "motif inversé « Aliment : quantité »",
			brut:  "Aubergines : 500 g",
			attendus: champsIngredient{
				quantite: nombre(500), unite: "g", aliment: "aubergine",
			},
		},
		// La contenance placée entre le contenant et l'aliment part en note :
		// quantity et unit sont déjà pris par « 1 boîte », et le schéma ne
		// porte qu'une mesure.
		{
			motif: "contenance après le contenant",
			brut:  "1 boîte de 796 ml (28 oz) de tomates broyées",
			attendus: champsIngredient{
				quantite: nombre(1), unite: "boîte", aliment: "tomates broyées",
				note: "796 ml ; 28 oz",
			},
		},
		// Deux défauts sur la même ligne : la marque de pluriel « (s) » n'est
		// plus prise pour une note, et le second terme de l'addition n'est
		// plus perdu — 250 + 200 font 450 parce que les deux termes portent
		// la même unité.
		{
			motif: "marque de pluriel et second terme d'une addition",
			brut:  "250 gramme(s) + 200 gramme(s) de Coulis de framboises",
			attendus: champsIngredient{
				quantite: nombre(450), unite: "g", aliment: "Coulis de framboises",
			},
		},
	}
	for _, c := range cas {
		t.Run(c.motif, func(t *testing.T) {
			verifieLesCinqChamps(t, champsLus(a.lit(c.brut)), c.attendus)
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

			relue := relit(t, app, ligne.Id)
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

	corrigee := relit(t, app, ligne.Id)
	corrigee.Set("food", "ail dégermé")
	corrigee.Set("quantity", 3)
	if err := app.Save(corrigee); err != nil {
		t.Fatalf("ré-enregistrement : %v", err)
	}

	relue := relit(t, app, ligne.Id)
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

	corrigee := relit(t, app, ligne.Id)
	corrigee.Set("food", "ail dégermé")
	if err := app.Save(corrigee); err != nil {
		t.Fatalf("correction manuelle : %v", err)
	}

	reecrite := relit(t, app, ligne.Id)
	reecrite.Set("raw", "1 c. à s. rase de sucre")
	if err := app.Save(reecrite); err != nil {
		t.Fatalf("changement de raw : %v", err)
	}

	relue := relit(t, app, ligne.Id)
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

	corrigee := relit(t, app, ligne.Id)
	corrigee.Set("food", "poivre")
	if err := app.Save(corrigee); err != nil {
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

	relue := relit(t, app, ligne.Id)
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
