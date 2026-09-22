package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// La migration corrective que les arbitrages du 21/09/2026 appellent : le
// vocabulaire des verdicts, et la cible d'une annotation ramenée à une clé
// naturelle.
//
// Ce que PATA-122 avait livré et qu'il faut reprendre : analyses_annotations
// .form était une relation vers analyses_formes, avec cascade. Or ces lignes
// appartiennent à une passe et sont recréées à chaque analyse — une annotation
// de forme ne serait donc pas retrouvée à la passe suivante, contre le critère
// de survie. La cible devient la forme brute, comme celle d'un groupe est
// l'aliment canonique.

// migrationDeLAnnotation : le fichier à défaire pour vérifier la descente.
const migrationDeLAnnotation = "1790092800_etabli_annotation.go"

// champsDeLAnnotation : le contrat de la collection après correction. Énumérés
// plutôt que comptés — en changer un doit rougir, pas passer inaperçu.
var champsDeLAnnotation = map[string]string{
	"analysis": core.FieldTypeRelation,
	// La cible d'une annotation de forme : la ligne brute, et non
	// l'identifiant d'une ligne recréée à chaque passe.
	"raw": core.FieldTypeText,
	// Celle d'une annotation de groupe : l'aliment canonique.
	"food": core.FieldTypeText,
	// Le verdict, par tags ouverts, dans un vocabulaire qui n'est pas celui du
	// carnet.
	"verdicts": core.FieldTypeRelation,
	// Les deux champs que la tâche sépare : l'un cite la ligne brute et ne
	// quitte jamais l'instance, l'autre porte le verdict sur la règle ou sur
	// l'entrée du lexique.
	"local_note":     core.FieldTypeText,
	"shareable_note": core.FieldTypeText,
	// La lecture attendue champ à champ, en miroir de analyses_formes.reading.
	// Du côté local : c'est la matière d'un jeu annoté, donc du corpus.
	"expected_reading": core.FieldTypeJSON,
	// L'empreinte de la passe jugée, recopiée : elle dit quel parser
	// l'annotation jugeait, et elle doit survivre à la passe suivante.
	"engine_version":  core.FieldTypeText,
	"lexicon_entries": core.FieldTypeNumber,
	// L'empreinte de la lecture jugée : c'est elle qui dit, à la passe
	// suivante, si le verdict tient encore ou si la cible doit être rejugée.
	"reading_digest": core.FieldTypeText,
	"created":        core.FieldTypeAutodate,
	"updated":        core.FieldTypeAutodate,
}

// champsDUnVerdict : le vocabulaire des verdicts, distinct de tags.
var champsDUnVerdict = map[string]string{
	"name": core.FieldTypeText,
	"slug": core.FieldTypeText,
	// Un tag est partageable par construction, mais il est saisi par un humain
	// qui peut y écrire du corpus : un tag neuf naît non validé, et la charge
	// partageable ne l'emporte pas tant qu'il ne l'est pas.
	"validated": core.FieldTypeBool,
	"created":   core.FieldTypeAutodate,
	"updated":   core.FieldTypeAutodate,
}

// --- Le schéma --------------------------------------------------------------

func TestLAnnotationPorteSesChampsApresCorrection(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	for champ, typeAttendu := range champsDeLAnnotation {
		pose := collection.Fields.GetByName(champ)
		if pose == nil {
			t.Errorf("analyses_annotations.%s absent", champ)
			continue
		}
		if pose.Type() != typeAttendu {
			t.Errorf("analyses_annotations.%s est un %s, attendu %s",
				champ, pose.Type(), typeAttendu)
		}
	}
}

// La relation vers analyses_formes est partie, et c'est le point de la
// migration : tant qu'elle est là, une annotation de forme disparaît avec la
// passe qu'elle jugeait.
func TestLAnnotationNeVisePlusUneLigneDeFormes(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	if champ := collection.Fields.GetByName("form"); champ != nil {
		t.Errorf("analyses_annotations.form existe encore (%s) : une annotation "+
			"qui référence une ligne recréée à chaque passe ne lui survit pas",
			champ.Type())
	}
}

func TestLeVocabulaireDesVerdictsPorteSesChamps(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("analyses_verdicts")
	if err != nil {
		t.Fatalf("collection analyses_verdicts : %v", err)
	}
	for champ, typeAttendu := range champsDUnVerdict {
		pose := collection.Fields.GetByName(champ)
		if pose == nil {
			t.Errorf("analyses_verdicts.%s absent", champ)
			continue
		}
		if pose.Type() != typeAttendu {
			t.Errorf("analyses_verdicts.%s est un %s, attendu %s",
				champ, pose.Type(), typeAttendu)
		}
	}
}

// Le vocabulaire des verdicts n'est pas celui du carnet : mélanger les deux
// ferait remonter « capture-trop » dans les filtres des recettes.
func TestLeVocabulaireDesVerdictsNestPasCollectionTags(t *testing.T) {
	app := baseNeuve(t)

	verdicts, err := app.FindCollectionByNameOrId("analyses_verdicts")
	if err != nil {
		t.Fatalf("collection analyses_verdicts : %v", err)
	}
	tags, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}
	if verdicts.Id == tags.Id {
		t.Error("analyses_verdicts et tags sont la même collection")
	}

	relation, ok := annotationRelation(t, app, "verdicts")
	if !ok {
		return
	}
	if relation.CollectionId != verdicts.Id {
		t.Errorf("analyses_annotations.verdicts vise %q, attendu analyses_verdicts",
			relation.CollectionId)
	}
	if relation.MaxSelect < 2 {
		t.Errorf("analyses_annotations.verdicts n'accepte que %d valeur(s) : "+
			"une annotation porte plusieurs verdicts", relation.MaxSelect)
	}
}

// Le slug est la clé d'unicité, comme sur tags : « Capture trop » et
// « capture-trop » doivent se rejoindre.
func TestLeSlugDUnVerdictEstUnique(t *testing.T) {
	app := baseNeuve(t)

	if err := ecritLeVerdict(app, "capture trop"); err != nil {
		t.Fatalf("premier verdict : %v", err)
	}
	if err := ecritLeVerdict(app, "capture trop"); err == nil {
		t.Error("deux verdicts de même slug ont été enregistrés")
	}
}

// Un verdict neuf naît non validé : la parade survit alors à un second
// annotateur, ce que le cloisonnement par le droit seul ne faisait pas.
func TestUnVerdictNaitNonValide(t *testing.T) {
	app := baseNeuve(t)

	if err := ecritLeVerdict(app, "capture trop"); err != nil {
		t.Fatalf("écriture du verdict : %v", err)
	}
	verdict, err := app.FindFirstRecordByData("analyses_verdicts", "slug", "capture-trop")
	if err != nil {
		t.Fatalf("relecture du verdict : %v", err)
	}
	if verdict.GetBool("validated") {
		t.Error("un verdict neuf naît validé : un humain pourrait y écrire du corpus")
	}
}

// --- Les règles d'accès -----------------------------------------------------

// Comme les trois autres collections de l'établi : cinq règles à nil, donc
// réservées au superutilisateur par l'API REST.
func TestLeVocabulaireDesVerdictsResteFermeALAPI(t *testing.T) {
	app := baseNeuve(t)

	for verbe, regle := range reglesDe(t, app, "analyses_verdicts") {
		if regle != nil {
			t.Errorf("analyses_verdicts.%sRule = %q, attendu nil", verbe, *regle)
		}
	}
}

// --- Les cascades -----------------------------------------------------------

// Le critère de survie, lu du côté du schéma : effacer la forme jugée
// n'emporte plus l'annotation, puisqu'elle ne la vise plus.
func TestUneAnnotationSurvitALaFormeQuElleJugeait(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	forme := formeNeuve(t, app, passe, "200 g de farine", "farine")
	annotation := annotationSurLaForme(t, app, passe, "200 g de farine")

	if err := app.Delete(forme); err != nil {
		t.Fatalf("suppression de la forme : %v", err)
	}

	relue, err := app.FindRecordById("analyses_annotations", annotation.Id)
	if err != nil {
		t.Fatalf("l'annotation a disparu avec la forme qu'elle jugeait : %v", err)
	}
	// Et elle désigne encore sa cible : une annotation qui survit sans savoir
	// ce qu'elle jugeait ne survit pas.
	if brut := relue.GetString("raw"); brut != "200 g de farine" {
		t.Errorf("l'annotation vise %q, attendu %q", brut, "200 g de farine")
	}
}

// Supprimer un verdict du vocabulaire ne doit pas emporter les annotations qui
// le portaient : le verdict est un mot, l'annotation est le jugement.
func TestSupprimerUnVerdictNemportePasLAnnotation(t *testing.T) {
	app := baseNeuve(t)
	passe := passeNeuve(t, app)
	formeNeuve(t, app, passe, "200 g de farine", "farine")
	verdict := verdictNeuf(t, app, "capture trop")

	annotation := annotationSurLaForme(t, app, passe, "200 g de farine")
	annotation.Set("verdicts", []string{verdict.Id})
	if err := app.Save(annotation); err != nil {
		t.Fatalf("pose du verdict sur l'annotation : %v", err)
	}

	if err := app.Delete(verdict); err != nil {
		t.Fatalf("suppression du verdict : %v", err)
	}

	if _, err := app.FindRecordById("analyses_annotations", annotation.Id); err != nil {
		t.Errorf("l'annotation a disparu avec le mot qui la qualifiait : %v", err)
	}
}

// --- Le down ----------------------------------------------------------------

// Une migration qui ne sait pas revenir en arrière n'est pas relisible.
func TestLeDownDeLAnnotationRetireLeVocabulaireEtLesChamps(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDeLAnnotation)

	if _, err := app.FindCollectionByNameOrId("analyses_verdicts"); err == nil {
		t.Error("analyses_verdicts survit au down")
	}

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations après le down : %v", err)
	}
	for _, champ := range []string{"raw", "verdicts", "expected_reading",
		"engine_version", "lexicon_entries", "reading_digest"} {
		if collection.Fields.GetByName(champ) != nil {
			t.Errorf("analyses_annotations.%s survit au down", champ)
		}
	}
	// Et la relation d'origine est rendue : un down qui laisse la collection
	// amputée n'est pas un retour en arrière.
	if collection.Fields.GetByName("form") == nil {
		t.Error("analyses_annotations.form n'est pas rendue par le down")
	}
}

// --- Montage ----------------------------------------------------------------

// annotationRelation relit un champ de relation de la collection des
// annotations, ou dit qu'il manque.
func annotationRelation(t *testing.T, app core.App, nom string) (*core.RelationField, bool) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	champ, ok := collection.Fields.GetByName(nom).(*core.RelationField)
	if !ok {
		t.Errorf("analyses_annotations.%s n'est pas une relation", nom)
		return nil, false
	}
	return champ, true
}

// ecritLeVerdict tente l'écriture et rend l'erreur telle quelle : le refus de
// l'index unique est la moitié de ce que cette collection garantit.
func ecritLeVerdict(app core.App, nom string) error {
	collection, err := app.FindCollectionByNameOrId("analyses_verdicts")
	if err != nil {
		return err
	}

	verdict := core.NewRecord(collection)
	verdict.Set("name", nom)
	verdict.Set("slug", motEnSlug(nom))
	return app.Save(verdict)
}

func verdictNeuf(t *testing.T, app core.App, nom string) *core.Record {
	t.Helper()

	if err := ecritLeVerdict(app, nom); err != nil {
		t.Fatalf("écriture du verdict %q : %v", nom, err)
	}
	verdict, err := app.FindFirstRecordByData("analyses_verdicts", "slug", motEnSlug(nom))
	if err != nil {
		t.Fatalf("relecture du verdict %q : %v", nom, err)
	}
	return verdict
}

// motEnSlug est la mise en slug dont ces tests ont besoin, et rien de plus :
// le paquet des migrations ne connaît pas la normalisation du serveur, qui est
// un hook de cmd/patachoo.
func motEnSlug(nom string) string {
	slug := []rune{}
	for _, lettre := range nom {
		if lettre == ' ' {
			slug = append(slug, '-')
			continue
		}
		slug = append(slug, lettre)
	}
	return string(slug)
}

// annotationSurLaForme pose une annotation dont la cible est la ligne brute.
func annotationSurLaForme(t *testing.T, app core.App, passe *core.Record, brut string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	annotation := core.NewRecord(collection)
	annotation.Set("analysis", passe.Id)
	annotation.Set("raw", brut)
	annotation.Set("local_note", "il fallait lire 200 g de farine T55")
	annotation.Set("shareable_note", "le motif se trompe sur ce gabarit")
	annotation.Set("engine_version", passe.GetString("engine_version"))
	annotation.Set("lexicon_entries", passe.GetInt("lexicon_entries"))
	if err := app.Save(annotation); err != nil {
		t.Fatalf("enregistrement de l'annotation : %v", err)
	}
	return annotation
}
