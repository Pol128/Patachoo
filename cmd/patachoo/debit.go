package main

import (
	"net/http"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// prioriteRattrapageDuDebit place notre rattrapage juste en amont du limiteur
// global de PocketBase, de façon à l'envelopper.
//
// Les middlewares du routeur et ceux de la route sont fondus dans un même
// crochet trié par priorité croissante (tools/router/router.go) : une priorité
// strictement inférieure enveloppe donc le limiteur, et notre e.Next() reçoit
// l'erreur qu'il remonte. Un cran, comme pour la session : plus l'écart est
// petit, moins il reste de place pour qu'un middleware tiers vienne s'y
// glisser.
const prioriteRattrapageDuDebit = apis.DefaultRateLimitMiddlewarePriority - 1

// rendLeDepassementEnHTML rattrape le refus du limiteur pour le rendre en page.
//
// PocketBase répond au dépassement par e.TooManyRequestsError(""), que
// router.ErrorHandler écrit en JSON — et cet écrivain-là n'est pas
// configurable. Or /connexion et /inscription sont des formulaires HTML
// ordinaires : l'utilisateur y verrait du JSON brut à la place de sa page.
// ErrorHandler s'abstient si la réponse est déjà écrite, d'où ce rattrapage,
// qui rend la page avant lui.
//
// Sur ces seules routes, et non sur le routeur : l'API REST parle JSON à ses
// clients, et lui rendre une page de connexion remplacerait une erreur lisible
// par du HTML qu'aucun client ne sait lire.
//
// Paramétré, parce que deux pages en ont besoin et que ce qui est subtil ici
// — la priorité, le e.Next(), le test sur le statut — doit tenir en un seul
// endroit. Le reste, message et gabarits, redescend au point de montage. Un
// identifiant de crochet par route : deux crochets de même identifiant sur le
// même hook se remplacent (tools/hook).
func rendLeDepassementEnHTML(id string, rendLaPage func(*core.RequestEvent) error) *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       id,
		Priority: prioriteRattrapageDuDebit,
		Func: func(e *core.RequestEvent) error {
			err := e.Next()
			if err == nil || router.ToApiError(err).Status != http.StatusTooManyRequests {
				return err
			}
			return rendLaPage(e)
		},
	}
}
