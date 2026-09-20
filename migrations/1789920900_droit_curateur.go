package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Le premier privilège du produit.
//
// Patachoo ne connaissait jusqu'ici aucune notion de droit : exigeUneSession()
// (cmd/patachoo/recettes.go) demande « y a-t-il quelqu'un ? », et rien d'autre.
// L'établi est le premier écran réservé, et il lui faut une notion qui servira
// bien au-delà de lui, dès que l'instance portera plusieurs comptes.
//
// Le droit est porté par users, et non par _superusers, parce qu'un
// superutilisateur n'a pas de session sur le site : /connexion cherche le
// compte par FindAuthRecordByEmail("users", …) et n'émet donc qu'un jeton
// users. Une garde qui exigerait IsSuperuser() livrerait une page que personne
// ne peut voir.
//
// Un booléen, et non un champ à valeurs ouvertes : il se lit d'un coup d'œil
// dans /_/, et il n'invite pas à inventer des rôles qu'on ne saurait plus
// énumérer. Sans préfixe is_, comme verified et open_registration.
//
// Faux par défaut, et donc faux sur tous les comptes d'une base déjà installée
// : le défaut d'un droit se choisit du côté qui refuse. C'est l'hébergeant qui
// coche la case dans /_/ sur le premier compte, en dix secondes, et aucune
// interface n'est écrite pour ça.
//
// Une migration nouvelle, et non le schéma initial retouché : une base déjà
// installée ne rejoue pas une migration qu'elle a passée, et resterait sans le
// champ.
//
// Les règles de users ne bougent pas. Le gel de l'auto-attribution est un hook
// de requête — fige(champCurateur) dans brancheLAcces (cmd/patachoo/acces.go)
// —, et non une règle : une règle n'est évaluée qu'en allant chercher la ligne,
// donc sur son état d'avant modification.
func init() {
	m.Register(func(app core.App) error {
		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}
		utilisateurs.Fields.Add(&core.BoolField{Name: "curator"})
		if err := app.Save(utilisateurs); err != nil {
			return fmt.Errorf("ajout de users.curator : %w", err)
		}
		return nil
	}, func(app core.App) error {
		utilisateurs, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return fmt.Errorf("collection users : %w", err)
		}
		utilisateurs.Fields.RemoveByName("curator")
		if err := app.Save(utilisateurs); err != nil {
			return fmt.Errorf("retrait de users.curator : %w", err)
		}
		return nil
	})
}
