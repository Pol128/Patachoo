package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// corpsMaximal borne une note de cuisine. C'est un journal d'expérience, pas
// un article : la borne existe pour que le champ ne devienne pas un moyen de
// gonfler la base (DOD.md §3, « Limites »). PocketBase compte en caractères,
// pas en octets (core/field_text.go) — un accent ne coûte donc pas double.
const corpsMaximal = 5000

// Un journal d'expérience sous chaque recette : de courtes notes datées et
// signées, écrites par les comptes qui ont cuisiné le plat.
//
// Une migration nouvelle, et non le schéma initial retouché : celui-ci est
// déjà appliqué sur les instances existantes, qui ne le rejoueraient pas et se
// retrouveraient sans la collection. Les migrations se défont dans l'ordre
// inverse, donc le down d'ici supprime comments avant que celui du schéma
// initial ne touche recipes.
func init() {
	m.Register(func(app core.App) error {
		recettes, err := app.FindCollectionByNameOrId("recipes")
		if err != nil {
			return fmt.Errorf("collection recipes : %w", err)
		}
		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}

		commentaires := core.NewBaseCollection("comments")
		commentaires.Fields.Add(
			&core.RelationField{
				Name:         "recipe",
				CollectionId: recettes.Id,
				MaxSelect:    1,
				Required:     true,
				// Supprimer une recette emporte ses notes, comme ses
				// ingrédients : une note sans son plat ne veut plus rien dire.
				CascadeDelete: true,
			},
			// author est obligatoire et en cascade, et c'est le seul point du
			// schéma qui ne se déduise pas du reste. Une note non signée
			// contredirait « datée et signée de son auteur » ; et puisqu'un
			// compte peut se supprimer lui-même — users porte
			// deleteRule = id = @request.auth.id —, ses notes partent avec lui
			// plutôt que de rester affichées sans nom.
			&core.RelationField{
				Name:          "author",
				CollectionId:  utilisateurs.Id,
				MaxSelect:     1,
				Required:      true,
				CascadeDelete: true,
			},
			// Du texte, pas du HTML : le même arbitrage que sur instructions,
			// tranché le 19/08/2026. Les gabarits l'échappent.
			&core.TextField{Name: "body", Required: true, Max: corpsMaximal, Presentable: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		// Les notes se lisent toujours recette par recette, du plus récent au
		// plus ancien : l'index suit cette lecture.
		commentaires.AddIndex("idx_comments_recipe_created", false, "recipe, created", "")

		// Les règles gardent l'API REST, que notre code ne traverse pas. Les
		// recettes sont partagées, les notes aussi : tout compte connecté les
		// lit. Écrire, c'est écrire en son nom ; corriger ou retirer reste à
		// qui a écrit.
		//
		// La règle de création rend author infalsifiable à la création, et à
		// elle seule : PocketBase n'évalue UpdateRule qu'en allant chercher la
		// ligne, donc sur son état d'avant modification. Un compte retournait
		// ainsi sa propre note au nom d'un autre — la règle voyait une note qui
		// était bien la sienne, le changement d'author venait après (PATA-63).
		// author et recipe sont donc figés à la modification par un hook, fige()
		// dans cmd/patachoo/acces.go, comme recipes.created_by depuis PATA-35.
		// Les règles ci-dessous restent en place : le hook couvre ce qu'elles
		// laissent passer, il ne les remplace pas.
		//
		// « author = @request.auth.id » n'a pas besoin du premier terme que
		// porte la règle de suppression des recettes : author est obligatoire,
		// une note ne peut pas le porter vide, donc la comparaison de "" à ""
		// n'a pas lieu.
		commentaires.ListRule = types.Pointer(`@request.auth.id != ""`)
		commentaires.ViewRule = types.Pointer(`@request.auth.id != ""`)
		commentaires.CreateRule = types.Pointer(`@request.auth.id != "" && author = @request.auth.id`)
		commentaires.UpdateRule = types.Pointer(`author = @request.auth.id`)
		commentaires.DeleteRule = types.Pointer(`author = @request.auth.id`)

		if err := app.Save(commentaires); err != nil {
			return fmt.Errorf("comments : %w", err)
		}
		return nil
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("comments")
		if err != nil {
			return nil // déjà absente : rien à défaire
		}
		if err := app.Delete(collection); err != nil {
			return fmt.Errorf("suppression de comments : %w", err)
		}
		return nil
	})
}
