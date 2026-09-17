package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// etiquetteDebitImport est ce que le limiteur global de PocketBase cherche sur
// la route de l'import unitaire : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// La forme longue, et non le chemin nu : une règle « /recettes/importer »
// plafonnerait aussi le GET de la page de saisie, c'est-à-dire la page même que
// le refusé doit voir.
//
// L'étiquette du lot, « POST /recettes/importer/lot », commence par celle-ci et
// n'en est pas attrapée pour autant : FindRateLimitRule (core/settings_model.go)
// cherche d'abord une égalité, et ne cherche un préfixe que pour les étiquettes
// finissant par « / ». Les deux plafonds restent donc distincts.
//
// Posée en constante parce que la descente doit retirer exactement ce que la
// montée a ajouté, et qu'une étiquette recopiée diverge le jour où on la
// change.
const etiquetteDebitImport = "POST /recettes/importer"

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// RateLimits.Enabled n'est pas touché ici : 1788993000_debit_connexion
		// l'a déjà mis à vrai, et le remettre à faux à la descente de celle-ci
		// rouvrirait la connexion, l'inscription, le lancement d'un lot et
		// l'authentification par l'API. Une migration qui défait le travail de
		// quatre autres n'est pas réversible, elle est destructrice.
		//
		// 10 imports par minute, par adresse. Chaque import entrant produit
		// deux requêtes sortantes — le robots.txt de l'hôte, puis la page —,
		// émises sous notre adresse et sous notre nom : c'est le site visé qui
		// paie l'abus, et l'hébergeant qui reçoit la plainte. Dix par minute
		// laissent largement la place à quelqu'un qui colle des adresses une à
		// une, et ferment la boucle qui martèle.
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
			Label:       etiquetteDebitImport,
			MaxRequests: 10,
			Duration:    60,
			Audience:    core.RateLimitRuleAudienceAuth,
		})

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur l'import unitaire : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// Retirer la nôtre par son étiquette, et non vider la liste : celles de
		// la connexion, de l'inscription, du lot et de l'API, les quatre de
		// PocketBase et toute règle qu'un humain aurait ajoutée dans /_/
		// restent.
		reglages.RateLimits.Rules = slices.DeleteFunc(reglages.RateLimits.Rules,
			func(regle core.RateLimitRule) bool { return regle.Label == etiquetteDebitImport })

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur l'import unitaire : %w", err)
		}
		return nil
	})
}
