package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// etiquetteDebitInscription est ce que le limiteur global de PocketBase cherche
// sur notre route, comme etiquetteDebitConnexion sur la sienne :
// defaultRateLimitLabels rend « <méthode> <chemin> » puis « <chemin> »
// (apis/middlewares_rate_limit.go).
//
// Les quatre règles livrées par PocketBase ne rattrapaient pas celle-ci :
// *:auth et *:create sont des étiquettes de collection, posées sur les seules
// routes /api/collections/…, et /api/ est un préfixe qui ne couvre pas
// /inscription. Aucun plafond ne s'appliquait donc, et un visiteur sans compte
// remplissait la table users aussi vite que le serveur sait hacher.
const etiquetteDebitInscription = "POST /inscription"

// Dix inscriptions par heure et par adresse, là où la connexion en autorise
// cinq par minute.
//
// Une seule règle s'applique par étiquette — FindRateLimitRule rend la première
// qui correspond (core/settings_model.go) —, donc on ne peut pas empiler « 10
// par heure et 3 par minute » : il faut choisir une fenêtre, et c'est la longue
// qui protège la table users. Une minute laisserait 7 200 comptes par nuit et
// par adresse ; l'heure en laisse 240. Et dix par heure ne gênent ni une maison
// derrière un NAT, ni quelqu'un qui se reprend à plusieurs fois sur un mot de
// passe trop court ou un courriel déjà pris — ces échecs-là comptent aussi,
// puisque le limiteur compte les requêtes et non les comptes créés.
//
// La fenêtre est plus longue que l'intervalle de nettoyage passé à
// newRateLimiter (1800 s), et c'est sans conséquence : hasExpired ajoute
// l'intervalle avant de comparer, donc un compteur en cours n'est pas balayé.
const (
	seuilDebitInscription   = 10
	fenetreDebitInscription = 3600
)

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// RateLimits.Enabled n'est pas reposé ici : 1788993000_debit_connexion
		// l'a déjà mis à vrai, et une migration déjà jouée sur une instance ne
		// se rejoue pas. Le poser une seconde fois ne nuirait pas, mais dirait
		// que cette migration-ci arme le mécanisme, alors qu'elle ne fait qu'y
		// ajouter une règle.

		// Ajout, et non remplacement, comme pour la connexion : les quatre
		// règles livrées protègent l'API REST, et la nôtre protège une page.
		//
		// Audience « tous », et non « invités » : checkRateLimit sort sans rien
		// compter quand l'audience ne correspond pas
		// (apis/middlewares_rate_limit.go). Or l'attaque que cette règle arrête
		// fabrique des comptes — son premier succès lui donnerait le jeton qui,
		// sous « invités », la ferait passer sous le plafond pour tous les
		// suivants. Un compte déjà connecté n'a de toute façon rien à faire sur
		// cette route.
		reglages.RateLimits.Rules = append(reglages.RateLimits.Rules, core.RateLimitRule{
			Label:       etiquetteDebitInscription,
			MaxRequests: seuilDebitInscription,
			Duration:    fenetreDebitInscription,
			Audience:    core.RateLimitRuleAudienceAll,
		})

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur l'inscription : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// Sa règle, et rien d'autre. RateLimits.Enabled reste à vrai : ce n'est
		// pas cette migration qui l'a armé, et l'éteindre ici désarmerait du
		// même coup le plafond de POST /connexion et les quatre règles de
		// PocketBase — un down qui ouvrirait deux portes de plus qu'il n'en
		// avait fermé.
		reglages.RateLimits.Rules = slices.DeleteFunc(reglages.RateLimits.Rules,
			func(regle core.RateLimitRule) bool { return regle.Label == etiquetteDebitInscription })

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur l'inscription : %w", err)
		}
		return nil
	})
}
