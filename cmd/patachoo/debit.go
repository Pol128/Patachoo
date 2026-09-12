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

// borneDuCorpsRattrape borne ce qu'un rattrapage lit du corps qu'il refuse.
//
// Un rattrapage enveloppe le limiteur ; il enveloppe donc aussi tout ce que le
// limiteur enveloppait, apis.BodyLimit compris — branché à
// DefaultRateLimitMiddlewarePriority + 10, soit un cran à l'intérieur. Le
// plafond tombé, le limiteur rend son erreur sans appeler e.Next() : le
// garde-fou de taille n'a jamais tourné, et une page de refus qui relit la
// saisie travaillerait sur un corps que personne ne borne. Un multipart en
// chunked s'y déverse dans un fichier temporaire au fil de la lecture, et son
// Content-Length à -1 met aussi le contrôle optimiste hors jeu : celui qui a
// dépassé son quota obtiendrait, par le refus même, un chemin moins borné que
// celui qui reste dessous.
//
// Un mébioctet, là où la voie ordinaire en autorise trente-deux : il s'agit de
// reproposer une saisie, pas d'accepter un envoi. Au-delà, la lecture échoue et
// la saisie n'est pas reprise — la reprise est un confort, elle ne vaut pas un
// disque.
const borneDuCorpsRattrape = 1 << 20

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

			// Avant que rendLaPage n'ait la moindre chance de lire le corps :
			// c'est ici, et nulle part plus loin, qu'il est encore temps de le
			// borner.
			e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, borneDuCorpsRattrape)

			return rendLaPage(e)
		},
	}
}
