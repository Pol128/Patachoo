package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// etiquetteDebitEtabli est ce que le limiteur global de PocketBase cherche sur
// la route de lancement d'une analyse : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// La forme longue, et non le chemin nu : le GET de la page n'écrit rien, et le
// plafonner mettrait hors de portée de celui qui vient de dépasser son quota la
// page même qui le lui dit — avec, en plus, le suivi de la passe en cours.
//
// Posée en constante parce que la descente doit retirer exactement ce que la
// montée a ajouté, et qu'une étiquette recopiée diverge le jour où on la change.
const etiquetteDebitEtabli = "POST /etabli"

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// RateLimits.Enabled n'est pas touché ici : 1788993000_debit_connexion
		// l'a déjà mis à vrai, et le remettre à faux à la descente de celle-ci
		// rouvrirait la route de connexion. Une migration qui défait le travail
		// d'une autre n'est pas réversible, elle est destructrice.
		//
		// Ce que cette règle borne n'est pas le nombre d'analyses — l'ouvrier
		// n'en mène qu'une à la fois, et refuse la seconde — mais le volume que
		// les requêtes font tenir en mémoire avant ce refus. Le POST lit son
		// corps sous le plafond de l'analyse, 32 Mio, là où les autres
		// formulaires du produit s'arrêtent à memoireMaxFormulaire ; s'y
		// ajoutent la recopie que le routeur fait de tout corps lu et la
		// lecture du corpus par le gestionnaire. La cible de DOD.md est un
		// Raspberry Pi.
		//
		// 5 lancements par minute, par adresse : le seuil du lot et celui de la
		// connexion, pour n'avoir qu'un ordre de grandeur à retenir. Large pour
		// l'usage — on lance une passe, on la regarde tourner — et il laisse la
		// place aux reprises qu'un refus de formulaire appelle : source
		// manquante, corpus vide, corps hors plafond.
		//
		// Audience « @auth » : la route est derrière exigeUnCurateur, qui passe
		// avant la lecture du corps. Un client sans session, ou sans le droit,
		// est refusé sans qu'un octet soit lu — il ne peut donc pas imposer ce
		// coût, et ce plafond-ci n'a pas à le viser. Le cookie est recopié en
		// en-tête avant pbLoadAuthToken, lui-même avant le limiteur, donc e.Auth
		// est peuplé quand la règle est cherchée.
		//
		// L'audience dit à *qui* la règle s'applique, pas ce qu'elle compte :
		// checkRateLimit compte par e.RealIP() quelle que soit l'audience. Ce
		// plafond est donc par adresse, et non par compte.
		reglages.RateLimits.Rules = append(reglages.RateLimits.Rules, core.RateLimitRule{
			Label:       etiquetteDebitEtabli,
			MaxRequests: 5,
			Duration:    60,
			Audience:    core.RateLimitRuleAudienceAuth,
		})

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur le lancement d'une analyse : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// Retirer la nôtre par son étiquette, et non vider la liste : celles de
		// la connexion, de l'inscription, du lot et de l'import, les quatre de
		// PocketBase et toute règle qu'un humain aurait ajoutée dans /_/ restent.
		reglages.RateLimits.Rules = slices.DeleteFunc(reglages.RateLimits.Rules,
			func(regle core.RateLimitRule) bool { return regle.Label == etiquetteDebitEtabli })

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur le lancement d'une analyse : %w", err)
		}
		return nil
	})
}
