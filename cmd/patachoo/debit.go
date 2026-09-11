package main

import (
	"net/http"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// prioriteRattrapageDuDebit place nos rattrapages juste en amont du limiteur
// global de PocketBase, de façon à l'envelopper.
//
// Les middlewares du routeur et ceux de la route sont fondus dans un même
// crochet trié par priorité croissante (tools/router/router.go) : une priorité
// strictement inférieure enveloppe donc le limiteur, et notre e.Next() reçoit
// l'erreur qu'il remonte. Un cran, comme pour la session : plus l'écart est
// petit, moins il reste de place pour qu'un middleware tiers vienne s'y
// glisser.
const prioriteRattrapageDuDebit = apis.DefaultRateLimitMiddlewarePriority - 1

// rattrapeLeDepassement rend en page le refus du limiteur, au lieu du JSON.
//
// PocketBase répond au dépassement par e.TooManyRequestsError(""), que
// router.ErrorHandler écrit en JSON — et cet écrivain-là n'est pas
// configurable. Or nos formulaires sont des pages HTML ordinaires :
// l'utilisateur verrait du JSON brut à la place de la sienne. ErrorHandler
// s'abstient si la réponse est déjà écrite, d'où ce rattrapage, qui rend la
// page avant lui.
//
// Posé route par route, et jamais sur le routeur : l'API REST parle JSON à ses
// clients, et lui rendre une de nos pages remplacerait une erreur lisible par
// du HTML qu'aucun client ne sait lire. Chaque route passe donc son propre
// identifiant de crochet — deux gestionnaires qui partageraient le leur, le
// second remplacerait le premier (tools/hook).
//
// Seul le StatusTooManyRequests est rattrapé : tout le reste ressort tel quel,
// et une erreur qui n'est pas un dépassement ne doit pas se déguiser en page.
func rattrapeLeDepassement(id string, rendLaPage func(*core.RequestEvent) error) *hook.Handler[*core.RequestEvent] {
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
