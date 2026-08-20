package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Les recettes sont partagées : tout compte connecté les lit, les crée et les
// modifie. Seule la suppression reste à qui a ajouté la recette — on travaille
// à plusieurs sur un même livre, on ne jette pas le travail d'un autre.
//
// Une migration nouvelle, et non le schéma initial retouché : une base déjà
// installée n'appliquerait pas une migration qu'elle a déjà passée, et se
// retrouverait sans règles.
func init() {
	m.Register(func(app core.App) error {
		// Le premier terme de la règle de suppression n'est pas décoratif :
		// created_by n'est pas Required, et une recette peut le porter vide.
		// Réduite à « created_by = @request.auth.id », la règle comparerait ""
		// à "" pour une requête non authentifiée, et lui accorderait la
		// suppression.
		connecte := types.Pointer(`@request.auth.id != ""`)
		auteur := types.Pointer(`@request.auth.id != "" && created_by = @request.auth.id`)

		if err := ecritLesRegles(app, "recipes", connecte, connecte, connecte, connecte, auteur); err != nil {
			return err
		}

		// Un ingrédient n'a pas d'auteur propre : il fait partie d'une recette
		// que tout compte connecté peut modifier, et retirer une ligne est une
		// modification. La suppression de la recette les emporte déjà, par la
		// cascade posée sur le champ recipe.
		return ecritLesRegles(app, "ingredients", connecte, connecte, connecte, connecte, connecte)
	}, func(app core.App) error {
		// nil, et non "" : une règle vide est publique, l'absence de règle
		// réserve la collection au superuser. C'est l'état d'avant.
		for _, nom := range []string{"recipes", "ingredients"} {
			if err := ecritLesRegles(app, nom, nil, nil, nil, nil, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// ecritLesRegles pose les cinq règles d'une collection, dans l'ordre où
// l'interface d'administration les présente.
func ecritLesRegles(app core.App, nom string, liste, vue, creation, modification, suppression *string) error {
	collection, err := app.FindCollectionByNameOrId(nom)
	if err != nil {
		return fmt.Errorf("collection %s : %w", nom, err)
	}

	collection.ListRule = liste
	collection.ViewRule = vue
	collection.CreateRule = creation
	collection.UpdateRule = modification
	collection.DeleteRule = suppression

	if err := app.Save(collection); err != nil {
		return fmt.Errorf("règles de %s : %w", nom, err)
	}
	return nil
}
