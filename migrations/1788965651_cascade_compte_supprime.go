package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Un compte supprimé emporte ce qu'il a saisi : ses recettes et ses lots
// d'import. C'est l'arbitrage rendu le 09/09/2026, et la perte est assumée —
// une recette saisie par un compte qui part disparaît, même si d'autres
// l'utilisaient. Elle est réversible : le jour où ça gêne, le refus de
// supprimer un compte qui porte du contenu se pose par-dessus, et cette
// migration-ci n'aura pas à être défaite.
//
// Les trois relations vers users se comportent enfin pareil : comments.author
// est en cascade depuis PATA-38, avec exactement ce raisonnement.
//
// Une migration nouvelle, et non le schéma initial ni celui du lot retouchés :
// une base déjà installée ne rejoue pas une migration qu'elle a passée, et
// resterait sans le correctif. C'est la règle que les deux fichiers énoncent
// eux-mêmes en commentaire.
//
// Ce que la migration ne fait pas : toucher aux lignes déjà en base. La
// cascade s'applique au moment de la suppression, PocketBase ne repasse pas
// sur l'existant. Une base où un compte est parti avant cette migration garde
// son contenu, désormais sans auteur.
func init() {
	m.Register(func(app core.App) error {
		return poseLaCascadeSurLAuteur(app, true)
	}, func(app core.App) error {
		return poseLaCascadeSurLAuteur(app, false)
	})
}

// poseLaCascadeSurLAuteur écrit le drapeau sur les deux relations created_by
// vers users, et ne touche à rien d'autre du champ — en particulier pas à
// Required, qui reste faux sur recipes.created_by : le champ est rempli par le
// code après coup, et une recette peut légitimement le porter vide.
func poseLaCascadeSurLAuteur(app core.App, cascade bool) error {
	for _, nom := range []string{"recipes", "imports"} {
		collection, err := app.FindCollectionByNameOrId(nom)
		if err != nil {
			return fmt.Errorf("collection %s : %w", nom, err)
		}

		champ, ok := collection.Fields.GetByName("created_by").(*core.RelationField)
		if !ok {
			return fmt.Errorf("%s.created_by n'est pas une relation", nom)
		}
		champ.CascadeDelete = cascade

		if err := app.Save(collection); err != nil {
			return fmt.Errorf("cascade sur %s.created_by : %w", nom, err)
		}
	}
	return nil
}
