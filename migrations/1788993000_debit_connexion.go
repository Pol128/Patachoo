package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// etiquetteDebitConnexion est ce que le limiteur global de PocketBase cherche
// sur notre route : defaultRateLimitLabels rend « <méthode> <chemin> » puis
// « <chemin> » (apis/middlewares_rate_limit.go). Une règle qui porte l'une des
// deux suffit ; rien n'est à étiqueter du côté de la route.
//
// Posée en constante parce que la descente doit retirer exactement ce que la
// montée a ajouté, et qu'une étiquette recopiée diverge le jour où on la
// change.
const etiquetteDebitConnexion = "POST /connexion"

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// Faux par défaut chez PocketBase (core/settings_model.go), donc aucune
		// règle ne s'applique — pas même les quatre qu'il livre. L'activer les
		// allume toutes du même coup sur l'API REST : c'est la conséquence
		// assumée du choix de passer par les réglages plutôt que d'écrire un
		// limiteur à nous.
		reglages.RateLimits.Enabled = true

		// Ajout, et non remplacement : les quatre règles livrées protègent
		// l'API REST, et les écraser en passant reviendrait à ouvrir une porte
		// en en fermant une autre.
		//
		// 5 tentatives par minute, par IP. Audience vide, c'est-à-dire « tous »
		// : une session déjà ouverte compte comme le reste, sans quoi un jeton
		// valable obtenu une fois suffirait à marteler la route.
		reglages.RateLimits.Rules = append(reglages.RateLimits.Rules, core.RateLimitRule{
			Label:       etiquetteDebitConnexion,
			MaxRequests: 5,
			Duration:    60,
			Audience:    core.RateLimitRuleAudienceAll,
		})

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur la connexion : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// L'état d'avant : le mécanisme éteint, et les seules règles de
		// PocketBase. Retirer la nôtre par son étiquette, et non vider la
		// liste — une règle qu'un humain aurait ajoutée dans /_/ n'a pas à
		// disparaître avec celle-ci.
		reglages.RateLimits.Enabled = false
		reglages.RateLimits.Rules = slices.DeleteFunc(reglages.RateLimits.Rules,
			func(regle core.RateLimitRule) bool { return regle.Label == etiquetteDebitConnexion })

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur la connexion : %w", err)
		}
		return nil
	})
}
