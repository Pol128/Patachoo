// Package migrations porte le schéma de Patachoo, versionné dans le dépôt.
//
// Le schéma n'est jamais créé à la main dans l'interface d'administration :
// c'est ce qui rend une instance reproductible, et le dépôt utilisable par un
// tiers qui part d'une base vide.
package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// typesDePlatInitiaux : une base fraîche doit être utilisable sans
// configuration. Une liste vide obligerait chacun à inventer la sienne avant de
// pouvoir enregistrer sa première recette.
//
// Ce sont des données, pas un schéma : elles se renomment et se complètent
// depuis l'administration, sans migration ni redéploiement. C'est précisément
// ce qui a fait préférer une collection à un champ « select ».
var typesDePlatInitiaux = []string{
	"Entrée",
	"Plat",
	"Dessert",
	"Apéritif",
	"Petit-déjeuner",
	"Goûter",
	"Boisson",
	"Sauce",
}

// saisons : là, un select suffit. L'ensemble est fermé, il ne bougera pas.
var saisons = []string{"printemps", "été", "automne", "hiver"}

func init() {
	m.Register(func(app core.App) error {
		tags := core.NewBaseCollection("tags")
		tags.Fields.Add(
			&core.TextField{Name: "name", Required: true, Presentable: true},
			&core.TextField{Name: "slug", Required: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		// Le slug est la clé d'unicité, pas le nom : « Végétarien » et
		// « végétarien » doivent se rejoindre, sinon on obtient trois tags pour
		// une même idée en une semaine.
		tags.AddIndex("idx_tags_slug", true, "slug", "")
		if err := app.Save(tags); err != nil {
			return fmt.Errorf("tags : %w", err)
		}

		typesDePlat := core.NewBaseCollection("meal_types")
		typesDePlat.Fields.Add(
			&core.TextField{Name: "name", Required: true, Presentable: true},
			&core.TextField{Name: "slug", Required: true},
			// position ordonne la barre de filtres : un tri alphabétique
			// mettrait « Dessert » avant « Entrée ».
			&core.NumberField{Name: "position", OnlyInt: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		typesDePlat.AddIndex("idx_meal_types_slug", true, "slug", "")
		if err := app.Save(typesDePlat); err != nil {
			return fmt.Errorf("meal_types : %w", err)
		}

		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}

		recettes := core.NewBaseCollection("recipes")
		recettes.Fields.Add(
			&core.TextField{Name: "title", Required: true, Presentable: true},
			// L'image est téléchargée et stockée, pas liée à chaud : une recette
			// ne doit pas dépendre d'un site tiers pour rester complète.
			&core.FileField{
				Name:      "image",
				MaxSelect: 1,
				MaxSize:   5 << 20,
				MimeTypes: []string{"image/jpeg", "image/png", "image/webp", "image/avif"},
				Thumbs:    []string{"300x200", "800x0"},
			},
			&core.NumberField{Name: "servings", OnlyInt: true},
			&core.NumberField{Name: "prep_time", OnlyInt: true}, // minutes
			&core.NumberField{Name: "cook_time", OnlyInt: true}, // minutes
			&core.EditorField{Name: "instructions"},
			// Vides sur une saisie manuelle, et c'est un cas normal : l'affichage
			// de la source doit s'effacer proprement, pas laisser un libellé vide.
			&core.URLField{Name: "source_url"},
			&core.TextField{Name: "source_name"},
			&core.RelationField{
				Name:         "meal_type",
				CollectionId: typesDePlat.Id,
				MaxSelect:    1,
			},
			&core.SelectField{Name: "seasons", Values: saisons, MaxSelect: len(saisons)},
			&core.RelationField{
				Name:         "tags",
				CollectionId: tags.Id,
				MaxSelect:    20,
			},
			// created_by n'est pas Required ici : le remplir revient au code qui
			// crée la recette, et les règles d'accès qui le protègent sont
			// PATA-4. Une contrainte posée avant son gardien se contourne.
			&core.RelationField{
				Name:         "created_by",
				CollectionId: utilisateurs.Id,
				MaxSelect:    1,
			},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(recettes); err != nil {
			return fmt.Errorf("recipes : %w", err)
		}

		ingredients := core.NewBaseCollection("ingredients")
		ingredients.Fields.Add(
			&core.RelationField{
				Name:          "recipe",
				CollectionId:  recettes.Id,
				MaxSelect:     1,
				Required:      true,
				CascadeDelete: true,
			},
			&core.NumberField{Name: "position", OnlyInt: true},
			// raw est le champ le plus important du schéma : la ligne telle que
			// le site la publie. Le parser peut se tromper ou ne rien rendre ;
			// la ligne brute reste alors affichable telle quelle. Une recette
			// dont l'analyse a échoué doit rester lisible, jamais amputée.
			&core.TextField{Name: "raw", Required: true, Presentable: true},
			// Sorties du parser, toutes facultatives — par construction.
			&core.NumberField{Name: "quantity"},
			&core.TextField{Name: "unit"},
			&core.TextField{Name: "food"},
			&core.TextField{Name: "note"},
			&core.BoolField{Name: "optional"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		// L'ordre des ingrédients compte, et il se lit toujours recette par
		// recette : l'index suit cette lecture.
		ingredients.AddIndex("idx_ingredients_recipe_position", false, "recipe, position", "")
		if err := app.Save(ingredients); err != nil {
			return fmt.Errorf("ingredients : %w", err)
		}

		for i, nom := range typesDePlatInitiaux {
			enregistrement := core.NewRecord(typesDePlat)
			enregistrement.Set("name", nom)
			enregistrement.Set("slug", slug(nom))
			enregistrement.Set("position", (i+1)*10)
			if err := app.Save(enregistrement); err != nil {
				return fmt.Errorf("type de plat %q : %w", nom, err)
			}
		}

		return nil
	}, func(app core.App) error {
		// L'ordre inverse de la création : ingredients dépend de recipes, qui
		// dépend de tags et meal_types.
		for _, nom := range []string{"ingredients", "recipes", "meal_types", "tags"} {
			collection, err := app.FindCollectionByNameOrId(nom)
			if err != nil {
				continue // déjà absente : rien à défaire
			}
			if err := app.Delete(collection); err != nil {
				return fmt.Errorf("suppression de %s : %w", nom, err)
			}
		}
		return nil
	})
}
