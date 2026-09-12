package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// etiquetteDebitLot est ce que le limiteur global de PocketBase cherche sur la
// route de lancement : defaultRateLimitLabels rend « <méthode> <chemin> » puis
// « <chemin> » (apis/middlewares_rate_limit.go).
//
// La forme longue, et non le chemin nu : le GET de la page de saisie n'écrit
// rien, et le plafonner mettrait le formulaire hors de portée de celui qui
// vient de dépasser son quota — précisément la page qu'il doit voir.
//
// Posée en constante parce que la descente doit retirer exactement ce que la
// montée a ajouté, et qu'une étiquette recopiée diverge le jour où on la
// change.
const etiquetteDebitLot = "POST /recettes/importer/lot"

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// RateLimits.Enabled n'est pas touché ici : 1788993000_debit_connexion
		// l'a déjà mis à vrai, et le remettre à faux à la descente de
		// celle-ci rouvrirait la route de connexion. Une migration qui défait
		// le travail d'une autre n'est pas réversible, elle est destructrice.
		//
		// 5 fournées par minute, par adresse. Le seuil de la connexion, pour
		// n'avoir qu'un ordre de grandeur à retenir, et déjà très large : cinq
		// fournées valent 2 500 URLs, là où l'ouvrier en consomme au plus une
		// par seconde et par hôte et mène les lots en série. Un humain qui
		// colle une liste en lance une, puis attend.
		//
		// Audience « @auth » : la route est derrière exigeUneSession, un
		// visiteur n'atteint jamais le gestionnaire. Le cookie est recopié en
		// en-tête avant pbLoadAuthToken, lui-même avant le limiteur, donc
		// e.Auth est peuplé quand la règle est cherchée — session par cookie
		// comprise.
		//
		// L'audience dit à *qui* la règle s'applique, pas ce qu'elle compte :
		// checkRateLimit compte par e.RealIP() quelle que soit l'audience. Ce
		// plafond est donc par adresse, et non par compte.
		reglages.RateLimits.Rules = append(reglages.RateLimits.Rules, core.RateLimitRule{
			Label:       etiquetteDebitLot,
			MaxRequests: 5,
			Duration:    60,
			Audience:    core.RateLimitRuleAudienceAuth,
		})

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur le lancement d'un lot : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// Retirer la nôtre par son étiquette, et non vider la liste : celle de
		// la connexion, les quatre de PocketBase et toute règle qu'un humain
		// aurait ajoutée dans /_/ restent.
		reglages.RateLimits.Rules = slices.DeleteFunc(reglages.RateLimits.Rules,
			func(regle core.RateLimitRule) bool { return regle.Label == etiquetteDebitLot })

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur le lancement d'un lot : %w", err)
		}
		return nil
	})
}
