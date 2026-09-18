package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// champDeLaReprise porte l'état coché d'une adresse en échec.
//
// Posé en constante parce que la descente doit retirer exactement ce que la
// montée a ajouté, et qu'un nom recopié diverge le jour où on le change.
const champDeLaReprise = "handled"

// Le rapport d'une fournée devient une liste de choses à faire : chaque adresse
// qui n'a pas abouti porte une case que l'utilisateur coche lui-même, et qu'il
// retrouve cochée en revenant sur la page.
//
// Le champ dit ce que l'utilisateur déclare, et non ce que l'ouvrier a constaté
// — import_urls.status porte déjà le sort de l'adresse, écrit par la machine.
// Les deux ne se confondent jamais : une adresse en échec reste en échec quand
// on la coche, elle est seulement reprise.
//
// Un booléen, et non une date de reprise : l'énoncé demande de cocher et de
// retrouver coché, et horodater serait une donnée que rien n'affiche.
//
// Une migration nouvelle, et non 1787329500_import_en_lot.go retouché : une
// base déjà installée ne rejoue pas une migration qu'elle a passée, et
// resterait sans le champ. C'est la règle que ce fichier-là énonce lui-même.
//
// Ce que la migration ne fait pas : toucher aux lignes déjà en base. Elles
// restent donc non cochées, ce qui est exact — personne n'a jamais déclaré les
// avoir reprises.
func init() {
	m.Register(func(app core.App) error {
		return poseLaReprise(app, true)
	}, func(app core.App) error {
		return poseLaReprise(app, false)
	})
}

// poseLaReprise ajoute le champ sur import_urls, ou le retire.
func poseLaReprise(app core.App, pose bool) error {
	collection, err := app.FindCollectionByNameOrId("import_urls")
	if err != nil {
		return fmt.Errorf("collection import_urls : %w", err)
	}

	if pose {
		collection.Fields.Add(&core.BoolField{Name: champDeLaReprise})
	} else {
		collection.Fields.RemoveByName(champDeLaReprise)
	}

	if err := app.Save(collection); err != nil {
		return fmt.Errorf("import_urls.%s : %w", champDeLaReprise, err)
	}
	return nil
}
