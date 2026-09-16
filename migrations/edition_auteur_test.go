package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// fichierDeLEditionAuteur sert au retour en arrière : defaitJusqua désigne une
// migration par son nom de fichier.
const fichierDeLEditionAuteur = "1789568400_edition_auteur.go"

// Les cinq règles que le down doit reposer, c'est-à-dire l'état de PATA-35
// exactement. Écrites en clair plutôt que relues depuis la migration : un test
// qui compare une constante à elle-même ne vérifie que lui-même.
var reglesDAvantLEditionAuteur = map[string]map[string]string{
	"recipes": {
		"list":   `@request.auth.id != ""`,
		"view":   `@request.auth.id != ""`,
		"create": `@request.auth.id != ""`,
		"update": `@request.auth.id != ""`,
		"delete": `@request.auth.id != "" && created_by = @request.auth.id`,
	},
	"ingredients": {
		"list":   `@request.auth.id != ""`,
		"view":   `@request.auth.id != ""`,
		"create": `@request.auth.id != ""`,
		"update": `@request.auth.id != ""`,
		"delete": `@request.auth.id != ""`,
	},
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer. Le down repose l'état d'avant,
// et lui seul — pas nil, qui est l'état d'avant PATA-35 et réserverait les deux
// collections au superuser.
func TestLeDownDeLEditionAuteurReposeLesReglesDePATA35(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLEditionAuteur)

	for nom, attendues := range reglesDAvantLEditionAuteur {
		posees := reglesDe(t, app, nom)
		for verbe, attendue := range attendues {
			posee := posees[verbe]
			if posee == nil {
				t.Errorf("%s.%sRule est nil après le down, attendu %q", nom, verbe, attendue)
				continue
			}
			if *posee != attendue {
				t.Errorf("%s.%sRule = %q après le down, attendu %q", nom, verbe, *posee, attendue)
			}
		}
	}
}

// Les ingrédients suivent la recette : leur écriture est celle de son auteur.
// Vider une recette ligne par ligne est le chemin destructeur que l'audit
// PATA-56 décrivait, et le fermer est la moitié de la décision.
func TestUnAutreCompteNEcritPasDansLesIngredientsDeLaRecetteDunAutre(t *testing.T) {
	app := baseNeuve(t)
	a := compteNeuf(t, app, "a@exemple.test")
	b := compteNeuf(t, app, "b@exemple.test")
	ligne := ingredientDe(t, app, recetteDe(t, app, a))

	regles := reglesDe(t, app, "ingredients")
	for _, verbe := range []string{"create", "update", "delete"} {
		if peutAcceder(t, app, ligne, b, regles[verbe]) {
			t.Errorf("le compte B obtient %s sur un ingrédient de la recette de A", verbe)
		}
		// Le sens de l'autorisation : une règle refermée sur personne
		// passerait la moitié précédente sans rien dire.
		if !peutAcceder(t, app, ligne, a, regles[verbe]) {
			t.Errorf("le compte A n'obtient plus %s sur un ingrédient de sa propre recette", verbe)
		}
	}
}

// La lecture reste partagée : c'est exactement ce que la décision du
// 16/09/2026 laisse ouvert, et une fermeture trop large l'emporterait sans
// qu'aucun autre test ne rougisse.
func TestLesIngredientsDeLaRecetteDunAutreRestentLisibles(t *testing.T) {
	app := baseNeuve(t)
	a := compteNeuf(t, app, "a@exemple.test")
	b := compteNeuf(t, app, "b@exemple.test")
	ligne := ingredientDe(t, app, recetteDe(t, app, a))

	regles := reglesDe(t, app, "ingredients")
	for _, verbe := range []string{"list", "view"} {
		if !peutAcceder(t, app, ligne, b, regles[verbe]) {
			t.Errorf("le compte B n'obtient pas %s sur un ingrédient de la recette de A", verbe)
		}
	}
}

// Une recette sans auteur n'appartient à personne, donc personne ne l'écrit —
// ni elle, ni ses ingrédients. C'est le premier terme des deux règles,
// transposé de la suppression : sans lui, elle appartiendrait à quiconque n'a
// pas d'identité non plus.
func TestUneRecetteSansAuteurNestModifiableParPersonne(t *testing.T) {
	app := baseNeuve(t)
	compte := compteNeuf(t, app, "compte@exemple.test")
	recette := recetteDe(t, app, nil)
	ligne := ingredientDe(t, app, recette)

	if peutAcceder(t, app, recette, compte, reglesDe(t, app, "recipes")["update"]) {
		t.Error("une recette dont created_by est vide se laisse modifier")
	}
	if peutAcceder(t, app, ligne, compte, reglesDe(t, app, "ingredients")["delete"]) {
		t.Error("un ingrédient d'une recette sans auteur se laisse supprimer")
	}
}

// ingredientDe rattache une ligne à la recette donnée. Un ingrédient orphelin
// n'existe pas : la relation est ce qui lui donne son auteur.
func ingredientDe(t *testing.T, app core.App, recette *core.Record) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatalf("collection ingredients : %v", err)
	}
	ligne := core.NewRecord(collection)
	ligne.Set("recipe", recette.Id)
	ligne.Set("raw", "3 pommes")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement de l'ingrédient : %v", err)
	}
	return ligne
}
