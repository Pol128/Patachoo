package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// Vide par défaut chez PocketBase, donc aucune sauvegarde automatique
		// sur une installation fraîche. Une nuit sur deux suffirait rarement à
		// consoler quelqu'un qui vient de perdre ses recettes : ce sera toutes
		// les nuits, à 3 h, heure du serveur.
		reglages.Backups.Cron = "0 3 * * *"
		// Trois nuits de recul. Au-delà, l'archive pèse le poids de pb_data à
		// chaque fois, et la place manque avant que le besoin n'apparaisse.
		reglages.Backups.CronMaxKeep = 3

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("réglages de sauvegarde : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// Les valeurs d'origine de PocketBase : cron vide — donc pas de
		// sauvegarde automatique — et une rotation à trois qui ne sert alors à
		// rien. La destination S3 n'est pas touchée : elle se règle dans /_/,
		// cette migration ne l'a jamais écrite.
		reglages.Backups.Cron = ""
		reglages.Backups.CronMaxKeep = 3

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("réglages de sauvegarde : %w", err)
		}
		return nil
	})
}
