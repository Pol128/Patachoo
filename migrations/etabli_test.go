package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// migrationDeLEtabli : le fichier à défaire pour vérifier la descente.
const migrationDeLEtabli = "1789931400_etabli_analyse.go"

// Les trois collections, dans l'ordre où le down doit les retirer : la
// dernière référence les précédentes.
var collectionsDeLEtabli = []string{"analyses_annotations", "analyses_formes", "analyses"}

// Les noms de collections, de champs et de valeurs sont un contrat entre les
// tâches de l'établi : l'ouvrier (PATA-197) écrit, les pages (PATA-199,
// PATA-200) lisent, l'annotation (PATA-201) pose les verdicts. Le test les
// énumère plutôt que de les compter — en changer un ici doit rougir, pas
// passer inaperçu.
var champsDeLEtabli = map[string]map[string]string{
	"analyses": {
		"source": core.FieldTypeSelect,
		"status": core.FieldTypeSelect,
		"lines":  core.FieldTypeNumber,
		"forms":  core.FieldTypeNumber,
		// L'empreinte de la passe : sans elle, une annotation ne dit plus quel
		// parser elle jugeait.
		"engine_version":  core.FieldTypeText,
		"lexicon_entries": core.FieldTypeNumber,
		"created":         core.FieldTypeAutodate,
		"updated":         core.FieldTypeAutodate,
		// La fin ne se déduit pas d'updated, que chaque écriture d'avancement
		// fait bouger.
		"finished": core.FieldTypeDate,
	},
	"analyses_formes": {
		"analysis":    core.FieldTypeRelation,
		"raw":         core.FieldTypeText,
		"occurrences": core.FieldTypeNumber,
		"pattern":     core.FieldTypeText,
		"reading":     core.FieldTypeJSON,
		"food":        core.FieldTypeText,
		"resolved":    core.FieldTypeBool,
		"category":    core.FieldTypeText,
		// Une liste de clés stables, et non une colonne par signal ni un
		// select fermé : la liste appartient au code qui la produit, et une
		// migration ne doit pas devenir le point de passage obligé de son
		// enrichissement.
		"signals": core.FieldTypeJSON,
		// La provenance de la première occurrence rencontrée : la recette
		// quand la source est la base de l'instance, le numéro de ligne quand
		// c'est un corpus fourni.
		"recipe": core.FieldTypeRelation,
		"line":   core.FieldTypeNumber,
	},
	"analyses_annotations": {
		"analysis": core.FieldTypeRelation,
		"form":     core.FieldTypeRelation,
		// L'agrégé n'étant pas stocké, une annotation de groupe ne vise pas un
		// enregistrement : elle vise l'aliment canonique, en texte.
		"food": core.FieldTypeText,
		// Deux champs séparés dès maintenant : l'un cite la ligne brute et ne
		// quitte jamais l'instance, l'autre porte le verdict sur la règle ou
		// sur l'entrée du lexique. Les séparer plus tard demanderait une
		// migration.
		"local_note":     core.FieldTypeText,
		"shareable_note": core.FieldTypeText,
		"created":        core.FieldTypeAutodate,
		"updated":        core.FieldTypeAutodate,
	},
}

// Les deux jeux de valeurs, dans l'ordre du contrat. Réécrits ici plutôt que
// lus depuis la migration : un test qui compare une variable à elle-même passe
// quoi qu'on y mette.
//
// Une passe a trois statuts là où un lot d'import n'en a que deux : un lot
// n'échoue pas en bloc, une passe d'analyse, si.
var (
	sourcesAttendues         = []string{"instance", "fourni"}
	statutsAttendusDUnePasse = []string{"en_cours", "termine", "echec"}
)

// Les trois déclencheurs qui tiennent l'index à jour : un par chemin
// d'écriture qui peut le faire diverger de la table.
var declencheursDeLEtabliAttendus = []string{
	"analyses_formes_fts_apres_insertion",
	"analyses_formes_fts_apres_maj",
	"analyses_formes_fts_apres_suppression",
}

// --- Le schéma --------------------------------------------------------------

func TestLesCollectionsDeLEtabliOntLeursChamps(t *testing.T) {
	app := baseNeuve(t)

	for nom, attendus := range champsDeLEtabli {
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

func TestLesValeursDUnePasseSontExactementCellesDuContrat(t *testing.T) {
	app := baseNeuve(t)

	for champ, attendues := range map[string][]string{
		"source": sourcesAttendues,
		"status": statutsAttendusDUnePasse,
	} {
		collection, err := app.FindCollectionByNameOrId("analyses")
		if err != nil {
			t.Fatalf("collection analyses : %v", err)
		}
		pose, ok := collection.Fields.GetByName(champ).(*core.SelectField)
		if !ok {
			t.Errorf("analyses.%s n'est pas un select", champ)
			continue
		}
		if !slices.Equal(pose.Values, attendues) {
			t.Errorf("analyses.%s accepte %v, attendu %v", champ, pose.Values, attendues)
		}
	}
}

// Un superutilisateur ne vit pas dans users : aucune des trois collections ne
// porte de created_by, et une relation vers users y serait fausse.
func TestAucuneCollectionDeLEtabliNePorteDAuteur(t *testing.T) {
	app := baseNeuve(t)

	for _, nom := range collectionsDeLEtabli {
		collection, err := app.FindCollectionByNameOrId(nom)
		if err != nil {
			t.Errorf("collection %s absente : %v", nom, err)
			continue
		}
		if collection.Fields.GetByName("created_by") != nil {
			t.Errorf("%s.created_by existe : la collection est réservée aux "+
				"superutilisateurs, qui ne vivent pas dans users", nom)
		}
	}
}

// --- Les règles d'accès -----------------------------------------------------

// Une collection neuve naît avec ses cinq règles à nil, et nil réserve au
// superutilisateur — là où "" ouvrirait à tout le monde. Le travail n'est donc
// pas de les poser, c'est de les tenir : ce test rougira le jour où une
// migration ultérieure les desserrera, décidé ou non.
func TestLesCollectionsDeLEtabliRestentFermeesALAPI(t *testing.T) {
	app := baseNeuve(t)

	for _, nom := range collectionsDeLEtabli {
		for verbe, regle := range reglesDe(t, app, nom) {
			if regle != nil {
				t.Errorf("%s.%sRule = %q, attendu nil : la collection doit rester "+
					"réservée au superutilisateur", nom, verbe, *regle)
			}
		}
	}
}

// Le sens du refus (DOD.md §3). Un compte ordinaire, authentifié — et même
// porteur du droit de curateur, qui ouvre les pages de l'établi et rien
// d'autre — n'obtient aucun des cinq verbes sur aucune des trois collections.
// Les pages lisent côté serveur par e.App, qui ne passe pas par les règles :
// l'API reste fermée même à un compte qui voit l'établi.
func TestUnCompteOrdinaireNObtientRienSurLEtabli(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	forme := formeNeuve(t, app, passe, "2 gousses d'ail", "ail")
	annotation := annotationNeuve(t, app, passe, forme, "cite la ligne brute", "verdict sur la règle")

	curateur := compteNeuf(t, app, "curateur@exemple.test")
	curateur.Set("curator", true)
	if err := app.Save(curateur); err != nil {
		t.Fatalf("droit de curateur : %v", err)
	}

	for nom, enregistrement := range map[string]*core.Record{
		"analyses":             passe,
		"analyses_formes":      forme,
		"analyses_annotations": annotation,
	} {
		for verbe, regle := range reglesDe(t, app, nom) {
			if peutAcceder(t, app, enregistrement, curateur, regle) {
				t.Errorf("un compte ordinaire obtient %s sur %s par l'API REST", verbe, nom)
			}
		}
	}
}

// --- L'index de recherche ---------------------------------------------------

func TestLIndexDeLEtabliEtSesDeclencheursSontCrees(t *testing.T) {
	app := baseNeuve(t)

	if !objetExiste(t, app, "table", "analyses_formes_fts") {
		t.Error("la table virtuelle analyses_formes_fts est absente")
	}
	for _, declencheur := range declencheursDeLEtabliAttendus {
		if !objetExiste(t, app, "trigger", declencheur) {
			t.Errorf("le déclencheur %s est absent", declencheur)
		}
	}
}

func TestLIndexDeLEtabliSuitLInsertionDUneForme(t *testing.T) {
	app := baseNeuve(t)

	formeNeuve(t, app, passeNeuve(t, app), "2 gousses d'ail", "ail")

	exigeFormeTrouvee(t, app, "gousses", "2 gousses d'ail")
	// L'aliment canonique est indexé lui aussi : il n'apparaît pas forcément
	// dans le brut, et c'est par lui que la page agrégée cherchera.
	exigeFormeTrouvee(t, app, "ail", "2 gousses d'ail")
}

func TestLIndexDeLEtabliSuitLaModificationDUneForme(t *testing.T) {
	app := baseNeuve(t)
	forme := formeNeuve(t, app, passeNeuve(t, app), "200 g de farine", "farine")

	forme.Set("raw", "200 g de sucre")
	forme.Set("food", "sucre")
	if err := app.Save(forme); err != nil {
		t.Fatalf("modification de la forme : %v", err)
	}

	exigeFormeTrouvee(t, app, "sucre", "200 g de sucre")
	exigeFormeIntrouvable(t, app, "farine")
}

func TestLIndexDeLEtabliSuitLaSuppressionDUneForme(t *testing.T) {
	app := baseNeuve(t)
	forme := formeNeuve(t, app, passeNeuve(t, app), "200 g de farine", "farine")

	if err := app.Delete(forme); err != nil {
		t.Fatalf("suppression de la forme : %v", err)
	}

	exigeFormeIntrouvable(t, app, "farine")
	if lignes := lignesDeLIndexDeLEtabli(t, app); lignes != 0 {
		t.Errorf("%d ligne(s) dans l'index après la suppression, attendu 0", lignes)
	}
}

// C'est ce que le LIKE de SQLite ne sait pas faire — « Crème » LIKE '%creme%'
// est faux —, et en français on tape volontiers sans accents ni majuscules.
func TestLIndexDeLEtabliReplieLesAccentsEtLaCasse(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	formeNeuve(t, app, passe, "20 cl de Crème fraîche", "crème fraîche")
	formeNeuve(t, app, passe, "1 feuille de Laurier", "laurier")

	exigeFormeTrouvee(t, app, "creme", "20 cl de Crème fraîche")
	exigeFormeTrouvee(t, app, "laurier", "1 feuille de Laurier")
}

// La suppression d'une passe passe par la cascade, donc par des suppressions
// de formes une à une : l'index ne doit rien en garder.
func TestLIndexDeLEtabliSuitLaSuppressionDUnePasse(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	formeNeuve(t, app, passe, "200 g de farine", "farine")
	formeNeuve(t, app, passe, "2 gousses d'ail", "ail")

	if err := app.Delete(passe); err != nil {
		t.Fatalf("suppression de la passe : %v", err)
	}

	if lignes := lignesDeLIndexDeLEtabli(t, app); lignes != 0 {
		t.Errorf("%d ligne(s) dans l'index après la suppression de la passe, attendu 0", lignes)
	}
}

// --- Une ligne par forme distincte ------------------------------------------

// « Une ligne par forme distincte, pas par occurrence » écrit dans le schéma
// plutôt que confié à la bonne volonté de l'appelant : 320 049 lignes brutes
// pour 112 297 formes sur le corpus de la forge.
func TestDeuxFoisLaMemeFormeDansUnePasseEstRefusee(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)

	if err := ecritLaForme(app, passe, "200 g de farine", "farine"); err != nil {
		t.Fatalf("première écriture : %v", err)
	}
	if err := ecritLaForme(app, passe, "200 g de farine", "farine"); err == nil {
		t.Error("la même forme est entrée deux fois dans la même passe")
	}
}

// Le cas normal d'une seconde passe sur le même corpus : l'unicité porte sur
// le couple, pas sur le brut seul.
func TestLaMemeFormePeutRevenirDansUneAutrePasse(t *testing.T) {
	app := baseNeuve(t)

	if err := ecritLaForme(app, passeNeuve(t, app), "200 g de farine", "farine"); err != nil {
		t.Fatalf("première passe : %v", err)
	}
	if err := ecritLaForme(app, passeNeuve(t, app), "200 g de farine", "farine"); err != nil {
		t.Errorf("seconde passe : %v — une nouvelle passe doit pouvoir relire le même corpus", err)
	}
}

// Une forme sans passe n'est rattachable à rien : ni lecture, ni agrégat.
func TestUneFormeSansPasseEstRefusee(t *testing.T) {
	app := baseNeuve(t)

	if err := ecritLaForme(app, nil, "200 g de farine", "farine"); err == nil {
		t.Error("une forme sans analysis a été enregistrée")
	}
}

// --- Les cascades -----------------------------------------------------------

func TestSupprimerUnePasseEmporteSesFormesEtSesAnnotations(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	forme := formeNeuve(t, app, passe, "200 g de farine", "farine")
	surLaForme := annotationNeuve(t, app, passe, forme, "la ligne citée", "le verdict")
	surLeGroupe := annotationNeuve(t, app, passe, nil, "la ligne citée", "le verdict")

	if err := app.Delete(passe); err != nil {
		t.Fatalf("suppression de la passe : %v", err)
	}

	exigeDisparu(t, app, "analyses_formes", forme.Id, "la forme de la passe")
	exigeDisparu(t, app, "analyses_annotations", surLaForme.Id, "l'annotation de forme")
	exigeDisparu(t, app, "analyses_annotations", surLeGroupe.Id, "l'annotation de groupe")
}

func TestSupprimerUneFormeEmporteSesAnnotations(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	forme := formeNeuve(t, app, passe, "200 g de farine", "farine")
	annotation := annotationNeuve(t, app, passe, forme, "la ligne citée", "le verdict")

	if err := app.Delete(forme); err != nil {
		t.Fatalf("suppression de la forme : %v", err)
	}

	exigeDisparu(t, app, "analyses_annotations", annotation.Id, "l'annotation de la forme")
}

// Le garde-fou de l'établi : il lit le carnet, il ne peut pas l'abîmer. Une
// cascade posée dans le mauvais sens sur analyses_formes.recipe ferait
// disparaître des recettes le jour où on efface une passe.
func TestLaRecetteCiteeParUneFormeEstFacultativeEtNonCascadante(t *testing.T) {
	app := baseNeuve(t)

	champ := relationDe(t, app, "analyses_formes", "recipe")
	if champ.Required {
		t.Error("analyses_formes.recipe est obligatoire : une forme issue d'un " +
			"corpus fourni ne désigne aucune recette")
	}
	if champ.CascadeDelete {
		t.Error("analyses_formes.recipe est en cascade : effacer une passe " +
			"emporterait des recettes du carnet")
	}
}

func TestSupprimerUnePasseNeToucheAucuneRecette(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteDe(t, app, nil)
	passe := passeNeuve(t, app)
	forme := formeNeuve(t, app, passe, "3 pommes", "pomme")
	forme.Set("recipe", recette.Id)
	if err := app.Save(forme); err != nil {
		t.Fatalf("provenance de la forme : %v", err)
	}

	if err := app.Delete(passe); err != nil {
		t.Fatalf("suppression de la passe : %v", err)
	}

	exigePresent(t, app, "recipes", recette.Id, "la recette citée par une forme")
}

// L'autre sens : la recette part, la forme reste. Une passe d'analyse est une
// mesure datée — l'effacer parce qu'une recette a disparu la rendrait fausse.
func TestSupprimerUneRecetteLaisseLaFormeEtVideSaProvenance(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteDe(t, app, nil)
	forme := formeNeuve(t, app, passeNeuve(t, app), "3 pommes", "pomme")
	forme.Set("recipe", recette.Id)
	if err := app.Save(forme); err != nil {
		t.Fatalf("provenance de la forme : %v", err)
	}

	if err := app.Delete(recette); err != nil {
		t.Fatalf("suppression de la recette : %v", err)
	}

	exigePresent(t, app, "analyses_formes", forme.Id, "la forme qui citait la recette")
	relue, err := app.FindRecordById("analyses_formes", forme.Id)
	if err != nil {
		t.Fatalf("relecture de la forme : %v", err)
	}
	if provenance := relue.GetString("recipe"); provenance != "" {
		t.Errorf("analyses_formes.recipe vaut encore %q après la suppression de la recette", provenance)
	}
}

// --- Le down ----------------------------------------------------------------

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer. Les déclencheurs compris —
// c'est ce qu'un down écrit à la main oublie.
func TestLeDownRetireLesCollectionsLIndexEtLesDeclencheursDeLEtabli(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDeLEtabli)

	for _, nom := range collectionsDeLEtabli {
		if _, err := app.FindCollectionByNameOrId(nom); err == nil {
			t.Errorf("collection %s toujours présente après le down", nom)
		}
	}
	if objetExiste(t, app, "table", "analyses_formes_fts") {
		t.Error("analyses_formes_fts survit au down")
	}
	for _, declencheur := range declencheursDeLEtabliAttendus {
		if objetExiste(t, app, "trigger", declencheur) {
			t.Errorf("le déclencheur %s survit au down", declencheur)
		}
	}
}

// --- Montage ----------------------------------------------------------------

// passeNeuve enregistre une passe en cours sur la base de l'instance, avec son
// empreinte : c'est ce que l'ouvrier (PATA-197) écrira au démarrage.
func passeNeuve(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses")
	if err != nil {
		t.Fatalf("collection analyses : %v", err)
	}
	passe := core.NewRecord(collection)
	passe.Set("source", "instance")
	passe.Set("status", "en_cours")
	passe.Set("engine_version", "v0.1.2-0.20260919135849-8bd041e0fc3e")
	passe.Set("lexicon_entries", 1234)
	if err := app.Save(passe); err != nil {
		t.Fatalf("enregistrement de la passe : %v", err)
	}
	return passe
}

func formeNeuve(t *testing.T, app core.App, passe *core.Record, brut, aliment string) *core.Record {
	t.Helper()

	if err := ecritLaForme(app, passe, brut, aliment); err != nil {
		t.Fatalf("enregistrement de la forme %q : %v", brut, err)
	}
	forme, err := app.FindFirstRecordByData("analyses_formes", "raw", brut)
	if err != nil {
		t.Fatalf("relecture de la forme %q : %v", brut, err)
	}
	return forme
}

// ecritLaForme tente l'écriture et rend l'erreur telle quelle : le refus de
// l'index unique et celui d'une forme sans passe sont la moitié de ce que
// cette collection garantit. Une passe nil laisse analysis vide, ce qui est
// justement le cas à refuser.
func ecritLaForme(app core.App, passe *core.Record, brut, aliment string) error {
	collection, err := app.FindCollectionByNameOrId("analyses_formes")
	if err != nil {
		return err
	}

	forme := core.NewRecord(collection)
	if passe != nil {
		forme.Set("analysis", passe.Id)
	}
	forme.Set("raw", brut)
	forme.Set("occurrences", 1)
	forme.Set("pattern", "quantite_unite_aliment")
	forme.Set("food", aliment)
	forme.Set("resolved", true)
	return app.Save(forme)
}

// annotationNeuve pose une annotation de forme quand forme est fournie, une
// annotation de groupe sinon — le groupe n'étant pas un enregistrement, il se
// désigne par son aliment canonique.
func annotationNeuve(t *testing.T, app core.App, passe, forme *core.Record, locale, partageable string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	annotation := core.NewRecord(collection)
	annotation.Set("analysis", passe.Id)
	if forme != nil {
		annotation.Set("form", forme.Id)
		annotation.Set("food", forme.GetString("food"))
	} else {
		annotation.Set("food", "farine")
	}
	annotation.Set("local_note", locale)
	annotation.Set("shareable_note", partageable)
	if err := app.Save(annotation); err != nil {
		t.Fatalf("enregistrement de l'annotation : %v", err)
	}
	return annotation
}

// --- Lecture de l'index -----------------------------------------------------

// brutsTrouves interroge l'index comme la page le fera : le mot est cité et
// suffixé de « * », donc cherché en préfixe.
func brutsTrouves(t *testing.T, app core.App, mot string) []string {
	t.Helper()

	bruts := []string{}
	err := app.DB().
		NewQuery("SELECT raw FROM analyses_formes_fts WHERE analyses_formes_fts MATCH {:q}").
		Bind(dbx.Params{"q": `"` + mot + `"*`}).
		Column(&bruts)
	if err != nil {
		t.Fatalf("recherche de %q dans l'index de l'établi : %v", mot, err)
	}
	return bruts
}

func exigeFormeTrouvee(t *testing.T, app core.App, mot string, brut string) {
	t.Helper()

	for _, trouve := range brutsTrouves(t, app, mot) {
		if trouve == brut {
			return
		}
	}
	t.Errorf("%q ne ramène pas %q dans l'index de l'établi", mot, brut)
}

func exigeFormeIntrouvable(t *testing.T, app core.App, mot string) {
	t.Helper()

	if trouves := brutsTrouves(t, app, mot); len(trouves) > 0 {
		t.Errorf("%q ramène encore %v dans l'index de l'établi", mot, trouves)
	}
}

func lignesDeLIndexDeLEtabli(t *testing.T, app core.App) int {
	t.Helper()

	var compte int
	if err := app.DB().NewQuery("SELECT count(*) FROM analyses_formes_fts").Row(&compte); err != nil {
		t.Fatalf("comptage de l'index de l'établi : %v", err)
	}
	return compte
}
