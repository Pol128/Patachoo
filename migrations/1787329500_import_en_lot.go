package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// statutsDUnLot : un lot est en cours, ou il est terminé. Rien entre les deux
// — un lot dont chaque ligne a un sort définitif est terminé, quel que soit ce
// sort.
var statutsDUnLot = []string{"en_cours", "termine"}

// statutsDUneURL : le sort d'une URL soumise. « deja_presente » n'est pas un
// échec, c'est un doublon reconnu ; le rapport de la sous-tâche 4 les compte
// séparément.
var statutsDUneURL = []string{"a_faire", "en_cours", "importee", "deja_presente", "echec"}

// L'état d'un import en lot, en base. Sans lui, un lot de 500 URLs coupé au
// milieu — redémarrage, coupure réseau — repartirait de zéro.
//
// Une migration nouvelle, et non le schéma initial retouché : une base déjà
// installée n'applique pas une migration qu'elle a déjà passée, et se
// retrouverait sans ces collections.
func init() {
	m.Register(func(app core.App) error {
		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}
		tags, err := app.FindCollectionByNameOrId("tags")
		if err != nil {
			return fmt.Errorf("collection tags : %w", err)
		}
		recettes, err := app.FindCollectionByNameOrId("recipes")
		if err != nil {
			return fmt.Errorf("collection recipes : %w", err)
		}

		lots := core.NewBaseCollection("imports")
		lots.Fields.Add(
			// Le compte qui a lancé le lot : c'est lui que porteront les
			// recettes créées.
			&core.RelationField{
				Name:         "created_by",
				CollectionId: utilisateurs.Id,
				MaxSelect:    1,
			},
			// Le tag de la fournée. Le champ est posé ici, l'enregistrement
			// est créé par la sous-tâche 2.
			&core.RelationField{
				Name:         "tag",
				CollectionId: tags.Id,
				MaxSelect:    1,
			},
			&core.SelectField{Name: "status", Values: statutsDUnLot, MaxSelect: 1},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(lots); err != nil {
			return fmt.Errorf("imports : %w", err)
		}

		urls := core.NewBaseCollection("import_urls")
		urls.Fields.Add(
			// Même construction que ingredients.recipe : supprimer un lot
			// emporte ses lignes, sinon la base garde la liste des URLs
			// soumises par un compte bien après qu'il a effacé son lot.
			&core.RelationField{
				Name:          "batch",
				CollectionId:  lots.Id,
				MaxSelect:     1,
				Required:      true,
				CascadeDelete: true,
			},
			// L'URL telle que l'utilisateur l'a soumise, avant toute
			// redirection : c'est celle qu'il reconnaîtra dans le rapport.
			&core.TextField{Name: "url", Required: true, Presentable: true},
			// L'ordre de saisie. La reprise et le rapport suivent la liste
			// donnée, pas un ordre d'insertion qui n'a de sens pour personne.
			&core.NumberField{Name: "position", OnlyInt: true},
			&core.SelectField{Name: "status", Values: statutsDUneURL, MaxSelect: 1},
			// La cause nommée d'un échec : les six de recuperation, les quatre
			// de jsonld. Un texte, et non un select — la liste appartient au
			// code qui les produit, et une migration ne doit pas devenir le
			// point de passage obligé de son enrichissement.
			&core.TextField{Name: "cause"},
			// Le code HTTP quand l'échec en porte un, pour que le rapport
			// distingue un 403 anti-bot d'un 404.
			&core.NumberField{Name: "code", OnlyInt: true},
			&core.RelationField{
				Name:         "recipe",
				CollectionId: recettes.Id,
				MaxSelect:    1,
			},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		// La lecture de l'ouvrier : la prochaine ligne « a_faire » d'un lot,
		// réclamée en boucle.
		urls.AddIndex("idx_import_urls_batch_status", false, "batch, status", "")
		// Une URL n'apparaît qu'une fois dans un lot. La même dans deux lots
		// distincts reste permise : c'est le cas normal d'un réimport.
		urls.AddIndex("idx_import_urls_batch_url", true, "batch, url", "")
		if err := app.Save(urls); err != nil {
			return fmt.Errorf("import_urls : %w", err)
		}

		// Les règles des deux collections restent nulles, donc réservées au
		// superuser par l'API REST. Ce n'est pas un oubli : Patachoo lit et
		// écrit cet état depuis son propre code Go (app.FindRecordById,
		// app.Save), qui ne passe pas par les règles. Les ouvrir n'apporterait
		// rien et exposerait la liste des URLs soumises par chaque compte.
		return nil
	}, func(app core.App) error {
		// import_urls d'abord : elle référence imports.
		for _, nom := range []string{"import_urls", "imports"} {
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
