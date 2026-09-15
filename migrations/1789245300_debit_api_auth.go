package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// etiquetteDebitAPIAuth est l'étiquette que checkCollectionRateLimit cherche sur
// POST /api/collections/users/auth-with-password. Il les construit dans l'ordre
// users:authWithPassword, users:auth, *:authWithPassword, *:auth, puis les
// étiquettes de chemin, et FindRateLimitRule retient la première qui a une règle
// (apis/middlewares_rate_limit.go, core/settings_model.go).
//
// users:auth est une étiquette primaire : elle l'emporte sur *:auth sans qu'on
// touche à celle-ci. C'est ce qui permet d'aligner l'authentification par l'API
// sur le plafond de POST /connexion sans rien relever d'autre.
//
// Écrire *:auth à la place serait une faute : elle couvre aussi
// POST /api/collections/_superusers/auth-with-password, qui tombe sur elle
// faute de règle _superusers:auth, et la relever à 5 par minute donnerait huit
// fois plus d'essais sur le compte qui peut tout.
//
// Posée en constante parce que la descente doit retirer exactement ce que la
// montée a ajouté, et qu'une étiquette recopiée diverge le jour où on la change.
const etiquetteDebitAPIAuth = "users:auth"

// Le plafond de POST /connexion, à l'identique : la route de l'API accepte les
// mêmes identifiants, sur la même collection, avec la même vérification de mot
// de passe. Un plafond qu'on croit à 5 et qui vaut 40 est un plafond dont on ne
// peut plus rien déduire.
//
// Les deux portes gardent des compteurs séparés — la clé du limiteur est
// collection.Id + Request.Pattern + étiquettes, par IP (checkRateLimit) —, donc
// une même adresse dispose de 5 essais par minute de chaque côté, soit 10 en
// tout. C'est assumé : les réglages de PocketBase ne savent pas partager un
// compteur entre deux routes, et le limiteur maison a été écarté en PATA-39.
const (
	seuilDebitAPIAuth   = 5
	fenetreDebitAPIAuth = 60
)

func init() {
	m.Register(func(app core.App) error {
		reglages := app.Settings()

		// RateLimits.Enabled n'est pas touché ici : 1788993000_debit_connexion
		// l'a déjà mis à vrai, et cette migration-ci ne fait qu'ajouter une
		// règle au mécanisme qu'une autre a armé.

		// Ajout, et non remplacement, comme ses voisines : *:auth reste en
		// place, avec ses valeurs d'origine, puisque c'est elle qui plafonne
		// encore l'authentification superuser.
		//
		// Audience vide, c'est-à-dire « tous » : une session déjà ouverte
		// compte comme le reste, sans quoi un jeton valable obtenu une fois
		// suffirait à marteler la route.
		reglages.RateLimits.Rules = append(reglages.RateLimits.Rules, core.RateLimitRule{
			Label:       etiquetteDebitAPIAuth,
			MaxRequests: seuilDebitAPIAuth,
			Duration:    fenetreDebitAPIAuth,
			Audience:    core.RateLimitRuleAudienceAll,
		})

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur l'authentification par l'API : %w", err)
		}
		return nil
	}, func(app core.App) error {
		reglages := app.Settings()

		// Sa règle, et rien d'autre. Retirée par son étiquette, et non en
		// vidant la liste : les quatre règles de PocketBase, celles des routes
		// voisines et toute règle qu'un humain aurait ajoutée dans /_/ restent.
		//
		// RateLimits.Enabled reste à vrai : ce n'est pas cette migration qui
		// l'a armé, et l'éteindre ici désarmerait du même coup tous les autres
		// plafonds — un down qui ouvrirait plusieurs portes de plus qu'il n'en
		// avait fermé.
		reglages.RateLimits.Rules = slices.DeleteFunc(reglages.RateLimits.Rules,
			func(regle core.RateLimitRule) bool { return regle.Label == etiquetteDebitAPIAuth })

		if err := app.Save(reglages); err != nil {
			return fmt.Errorf("plafond de débit sur l'authentification par l'API : %w", err)
		}
		return nil
	})
}
