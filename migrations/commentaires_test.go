package migrations

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// Les cinq règles de PATA-22, littéralement.
//
// Les recettes sont partagées, les notes qu'on y accroche aussi : tout compte
// connecté les lit. Écrire, en revanche, c'est écrire en son nom — et corriger
// ou retirer une note reste à qui l'a écrite. « author = @request.auth.id »
// n'a pas besoin du premier terme que porte la règle de suppression des
// recettes : author est obligatoire, une note ne peut pas le porter vide.
const (
	regleNoteLisible     = `@request.auth.id != ""`
	regleNoteSignee      = `@request.auth.id != "" && author = @request.auth.id`
	regleNoteDeSonAuteur = `author = @request.auth.id`
)

var reglesDesCommentaires = map[string]string{
	"list":   regleNoteLisible,
	"view":   regleNoteLisible,
	"create": regleNoteSignee,
	"update": regleNoteDeSonAuteur,
	"delete": regleNoteDeSonAuteur,
}

func TestLaCollectionDesCommentairesEstCreee(t *testing.T) {
	app := baseNeuve(t)

	if _, err := app.FindCollectionByNameOrId("comments"); err != nil {
		t.Fatalf("collection comments absente : %v", err)
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownRetireLaCollectionDesCommentaires(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDesCommentaires)

	if _, err := app.FindCollectionByNameOrId("comments"); err == nil {
		t.Error("comments existe encore après le down")
	}
	// Le reste de la base est rendu à son état précédent : les migrations se
	// défont dans l'ordre inverse, donc recipes est toujours là.
	if _, err := app.FindCollectionByNameOrId("recipes"); err != nil {
		t.Errorf("recipes a disparu avec comments : %v", err)
	}
}

// Une note sans recette, sans auteur ou sans texte n'est pas une note : les
// trois champs sont obligatoires, et les dates se posent toutes seules.
func TestLesTroisChampsDUneNoteSontObligatoires(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("comments")
	if err != nil {
		t.Fatalf("collection comments : %v", err)
	}

	obligatoires := map[string]bool{}
	for _, champ := range collection.Fields {
		if estObligatoire(champ) {
			obligatoires[champ.GetName()] = true
		}
	}

	for _, nom := range []string{"recipe", "author", "body"} {
		if !obligatoires[nom] {
			t.Errorf("%s n'est pas obligatoire", nom)
		}
	}

	for _, nom := range []string{"created", "updated"} {
		if _, ok := collection.Fields.GetByName(nom).(*core.AutodateField); !ok {
			t.Errorf("%s n'est pas une autodate", nom)
		}
	}
}

// Supprimer une recette emporte ses notes, comme ses ingrédients : sur des
// enregistrements réels, et non sur le seul drapeau du champ — c'est la base
// qui applique la cascade, pas le schéma qui la déclare.
func TestLesNotesSuiventLaRecetteSupprimee(t *testing.T) {
	app := baseNeuve(t)
	auteur := compteNeuf(t, app, "auteur@exemple.test")
	recette := recetteDe(t, app, auteur)
	note := noteDe(t, app, recette, auteur, "Trop cuit de dix minutes.")

	if err := app.Delete(recette); err != nil {
		t.Fatalf("suppression de la recette : %v", err)
	}

	if _, err := app.FindRecordById("comments", note.Id); err == nil {
		t.Error("la note survit à la recette supprimée")
	}
}

// L'autre sens de la cascade : un compte peut se supprimer lui-même par l'API
// que PocketBase expose déjà, et ses notes partent avec lui plutôt que de
// rester affichées sans nom.
func TestLesNotesSuiventLeCompteSupprime(t *testing.T) {
	app := baseNeuve(t)
	auteur := compteNeuf(t, app, "auteur@exemple.test")
	recette := recetteDe(t, app, auteur)
	note := noteDe(t, app, recette, auteur, "Trop cuit de dix minutes.")

	if err := app.Delete(auteur); err != nil {
		t.Fatalf("suppression du compte : %v", err)
	}

	if _, err := app.FindRecordById("comments", note.Id); err == nil {
		t.Error("la note survit au compte supprimé")
	}
	if _, err := app.FindRecordById("recipes", recette.Id); err != nil {
		t.Errorf("la recette a disparu avec son commentateur : %v", err)
	}
}

// La borne existe pour que le champ ne devienne pas un moyen de gonfler la
// base (DOD.md §3) : sans test, une valeur codée finit augmentée
// « temporairement ». Comptée en caractères, et non en octets — un accent ne
// coûte pas deux fois plus cher qu'une lettre.
func TestUnCorpsDePlusDeCinqMilleCaracteresEstRefuse(t *testing.T) {
	app := baseNeuve(t)
	auteur := compteNeuf(t, app, "auteur@exemple.test")
	recette := recetteDe(t, app, auteur)

	if err := ecritLaNote(app, recette, auteur, strings.Repeat("é", 5000)); err != nil {
		t.Errorf("une note de 5000 caractères est refusée : %v", err)
	}
	if err := ecritLaNote(app, recette, auteur, strings.Repeat("é", 5001)); err == nil {
		t.Error("une note de 5001 caractères est acceptée")
	}
}

// Les notes se lisent toujours recette par recette, du plus récent au plus
// ancien : l'index suit cette lecture.
func TestLIndexDesNotesExiste(t *testing.T) {
	app := baseNeuve(t)

	collection, err := app.FindCollectionByNameOrId("comments")
	if err != nil {
		t.Fatalf("collection comments : %v", err)
	}

	for _, index := range collection.Indexes {
		if strings.Contains(index, "idx_comments_recipe_created") {
			return
		}
	}
	t.Errorf("idx_comments_recipe_created absent : %v", collection.Indexes)
}

// La comparaison est littérale, caractère pour caractère : une règle
// « équivalente » réécrite à la main est une règle qu'il faut relire, et
// celle-ci est tout ce qui garde l'API REST, que notre code ne traverse pas.
func TestLesReglesDAccesDesNotesSontPosees(t *testing.T) {
	app := baseNeuve(t)

	posees := reglesDe(t, app, "comments")
	for verbe, attendue := range reglesDesCommentaires {
		posee := posees[verbe]
		if posee == nil {
			t.Errorf("comments.%sRule est nil : la collection reste réservée au superuser", verbe)
			continue
		}
		if *posee != attendue {
			t.Errorf("comments.%sRule = %q, attendu %q", verbe, *posee, attendue)
		}
	}
}

// Le down rend la collection au superuser : c'est l'état d'avant, et une
// collection laissée avec ses règles sans exister n'a pas de sens.
func TestLesReglesDesNotesDisparaissentAvecLaCollection(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, migrationDesCommentaires)

	if _, err := app.FindCollectionByNameOrId("comments"); err == nil {
		t.Error("comments existe encore après le down, avec ses règles")
	}
}

// Le sens du refus : ce qu'un compte ne peut pas faire. Sans authentification,
// rien — pas même lire une note.
func TestUnVisiteurNePeutRienSurUneNote(t *testing.T) {
	app := baseNeuve(t)
	auteur := compteNeuf(t, app, "auteur@exemple.test")
	note := noteDe(t, app, recetteDe(t, app, auteur), auteur, "Trop cuit.")

	for verbe, regle := range reglesDe(t, app, "comments") {
		if peutAcceder(t, app, note, nil, regle) {
			t.Errorf("un visiteur non authentifié obtient %s sur une note", verbe)
		}
	}
}

// « Modification et suppression réservées à l'auteur », écrit là où l'API le
// lit. Lire reste permis : les notes sont partagées comme les recettes.
func TestUnAutreCompteLitLaNoteMaisNeLaTouchePas(t *testing.T) {
	app := baseNeuve(t)
	auteur := compteNeuf(t, app, "auteur@exemple.test")
	autre := compteNeuf(t, app, "autre@exemple.test")
	note := noteDe(t, app, recetteDe(t, app, auteur), auteur, "Trop cuit.")

	regles := reglesDe(t, app, "comments")
	if !peutAcceder(t, app, note, autre, regles["view"]) {
		t.Error("un compte connecté ne lit pas la note d'un autre : les notes sont partagées")
	}
	if peutAcceder(t, app, note, autre, regles["update"]) {
		t.Error("un compte connecté modifie la note d'un autre")
	}
	if peutAcceder(t, app, note, autre, regles["delete"]) {
		t.Error("un compte connecté supprime la note d'un autre")
	}
}

// On ne poste pas au nom d'un autre : la règle de création rend author
// infalsifiable par l'API, sans hook.
func TestOnNeCreePasUneNoteAuNomDunAutre(t *testing.T) {
	app := baseNeuve(t)
	auteur := compteNeuf(t, app, "auteur@exemple.test")
	autre := compteNeuf(t, app, "autre@exemple.test")
	note := noteDe(t, app, recetteDe(t, app, auteur), auteur, "Trop cuit.")

	creation := reglesDe(t, app, "comments")["create"]
	if peutAcceder(t, app, note, autre, creation) {
		t.Error("un compte crée une note signée d'un autre")
	}
	if !peutAcceder(t, app, note, auteur, creation) {
		t.Error("un compte ne peut pas créer sa propre note")
	}
}

// --- Fixtures --------------------------------------------------------------

// migrationDesCommentaires : le fichier que defaitJusqua doit atteindre. Une
// constante plutôt qu'un littéral répété — le nom change le jour où le
// fichier est renommé, et un seul endroit ment alors.
const migrationDesCommentaires = "1788944400_commentaires.go"

// noteDe enregistre une note et fait échouer le test si elle est refusée.
func noteDe(t *testing.T, app core.App, recette, auteur *core.Record, corps string) *core.Record {
	t.Helper()

	if err := ecritLaNote(app, recette, auteur, corps); err != nil {
		t.Fatalf("enregistrement de la note : %v", err)
	}
	notes, err := app.FindAllRecords("comments")
	if err != nil || len(notes) == 0 {
		t.Fatalf("relecture des notes : %v", err)
	}
	return notes[len(notes)-1]
}

// ecritLaNote tente l'écriture et rend l'erreur : les tests de borne veulent
// le refus, pas l'arrêt du test.
func ecritLaNote(app core.App, recette, auteur *core.Record, corps string) error {
	collection, err := app.FindCollectionByNameOrId("comments")
	if err != nil {
		return err
	}

	note := core.NewRecord(collection)
	note.Set("recipe", recette.Id)
	note.Set("author", auteur.Id)
	note.Set("body", corps)
	return app.Save(note)
}
