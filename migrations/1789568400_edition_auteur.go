package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// L'édition d'une recette revient à qui l'a ajoutée, comme sa suppression.
//
// La décision du 16/09/2026, rendue sur le constat de l'audit PATA-56 : la
// règle de suppression protégeait l'enregistrement, pas ce qu'il contient. Tout
// compte connecté pouvait vider le titre, les instructions et la source d'une
// recette, puis en retirer les ingrédients ligne par ligne — il restait une
// fiche blanche que son auteur seul pouvait effacer, et lui seul à ne rien
// pouvoir récupérer. Le carnet reste partagé en lecture : on lit, on commente
// et on cherche dans tout, on n'écrit que sur le sien.
//
// Une migration nouvelle, et non 1787242977_regles_acces.go retouché : une base
// déjà installée ne rejoue pas une migration qu'elle a passée, et resterait
// sans le correctif. C'est la règle que ce fichier-là énonce lui-même.
//
// Les règles de collection gardent l'API REST, pas notre code : nos routes
// écrivent par txApp.Save, qui ne les applique pas. Le contrôle de propriété
// est donc transposé à la main dans laRecetteDemandee
// (cmd/patachoo/recettes.go), comme il l'est déjà dans laRecetteASupprimer.
// Sans ce second geste, cette migration désaccorderait le formulaire et l'API
// au lieu de fermer quoi que ce soit.
func init() {
	m.Register(func(app core.App) error {
		connecte := types.Pointer(`@request.auth.id != ""`)
		// Le premier terme n'est pas décoratif : created_by n'est pas Required,
		// et une recette peut le porter vide — importée avant PATA-35, ou
		// laissée par un compte parti avant la cascade de PATA-38. Réduite à
		// « created_by = @request.auth.id », la règle comparerait "" à "" pour
		// une requête non authentifiée et lui donnerait raison.
		auteur := types.Pointer(`@request.auth.id != "" && created_by = @request.auth.id`)

		// Seule Update change : lire, et créer la sienne, restent ouverts à
		// tout compte connecté. Delete portait déjà cette règle.
		if err := ecritLesRegles(app, "recipes", connecte, connecte, connecte, auteur, auteur); err != nil {
			return err
		}

		// Un ingrédient n'a pas d'auteur propre : il tient le sien de la
		// recette qui le porte. Fermer l'édition de la recette en laissant
		// ingredients ouvert ne fermerait rien — vider une recette ligne par
		// ligne est précisément le chemin que l'audit décrit.
		//
		// La relation est traversée dans la règle : « recipe.created_by » est
		// résolu par la requête que PocketBase construit pour l'évaluer.
		auteurDeLaRecette := types.Pointer(`@request.auth.id != "" && recipe.created_by = @request.auth.id`)
		return ecritLesRegles(app, "ingredients",
			connecte, connecte, auteurDeLaRecette, auteurDeLaRecette, auteurDeLaRecette)
	}, func(app core.App) error {
		// L'état d'avant, c'est-à-dire celui de PATA-35 — et non nil, qui est
		// l'état d'avant PATA-35 et réserverait les deux collections au
		// superuser. Un down qui ferme plus qu'il n'avait ouvert n'est pas un
		// down.
		connecte := types.Pointer(`@request.auth.id != ""`)
		auteur := types.Pointer(`@request.auth.id != "" && created_by = @request.auth.id`)

		if err := ecritLesRegles(app, "recipes", connecte, connecte, connecte, connecte, auteur); err != nil {
			return err
		}
		return ecritLesRegles(app, "ingredients", connecte, connecte, connecte, connecte, connecte)
	})
}
