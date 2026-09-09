package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Une seule porte pour l'inscription, et un verrou dessus.
//
// PocketBase livre users avec une règle de création vide — donc publique
// (pocketbase@v0.39.11/migrations/1640988000_init.go:321) : sur une base
// Patachoo fraîche, n'importe qui pouvait se créer un compte par
// POST /api/collections/users/records, sans passer par la moindre page à nous.
// La règle passe à nil, c'est-à-dire réservée au superuser, et toute
// inscription passe désormais par notre route, qui consulte le réglage.
//
// Le réglage lui-même vit dans settings, une collection à un seul
// enregistrement. Ses règles restent toutes à nil : notre code le lit sans
// passer par elles, et l'administration sur /_/ le modifie sans les lire non
// plus — c'est ce qui permet de basculer l'inscription sans redémarrage et
// sans écrire une page de réglages.
//
// Une migration nouvelle, et non le schéma initial retouché : une base déjà
// installée n'appliquerait pas une migration qu'elle a déjà passée, et
// garderait sa porte de derrière ouverte.
func init() {
	m.Register(func(app core.App) error {
		reglages := core.NewBaseCollection("settings")
		reglages.Fields.Add(
			// Fermée par défaut : le superuser existe déjà, il est créé en
			// ligne de commande, et il ouvre l'inscription quand il le veut.
			// L'inverse mettrait l'auto-hébergeant en retard sur le premier
			// passant.
			&core.BoolField{Name: "open_registration"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("settings : %w", err)
		}

		// L'enregistrement est posé ici, et non créé à la volée au premier
		// besoin : une base migrée doit porter son réglage, visible et
		// modifiable dans /_/ sans que personne ait à deviner qu'il faut
		// d'abord l'y créer.
		reglage := core.NewRecord(reglages)
		reglage.Set("open_registration", false)
		if err := app.Save(reglage); err != nil {
			return fmt.Errorf("enregistrement de settings : %w", err)
		}

		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}
		utilisateurs.CreateRule = nil
		if err := app.Save(utilisateurs); err != nil {
			return fmt.Errorf("verrouillage de users.createRule : %w", err)
		}
		return nil
	}, func(app core.App) error {
		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}
		// "" et non nil : une règle vide est publique, l'absence de règle
		// réserve la collection au superuser. C'est l'état d'avant, celui que
		// PocketBase livre.
		utilisateurs.CreateRule = types.Pointer("")
		if err := app.Save(utilisateurs); err != nil {
			return fmt.Errorf("rétablissement de users.createRule : %w", err)
		}

		reglages, err := app.FindCollectionByNameOrId("settings")
		if err != nil {
			return nil // déjà absente : rien à défaire
		}
		if err := app.Delete(reglages); err != nil {
			return fmt.Errorf("suppression de settings : %w", err)
		}
		return nil
	})
}
