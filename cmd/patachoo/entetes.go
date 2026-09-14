package main

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// Les en-têtes de réponse que Patachoo pose lui-même, en plus de ceux que
// PocketBase pose déjà (apis/middlewares.go, pbSecurityHeaders).

// cacheControlDesPages interdit à tout cache — partagé comme privé — de
// conserver une réponse rendue pour un compte.
//
// « private » dit qu'elle est propre à un utilisateur, « no-store » qu'elle ne
// se conserve pas du tout. Les deux et non « private » seul : sans no-store, un
// cache partagé mal réglé qui ignore private garderait quand même la page, et
// rien dans la réponse ne lui dirait que la clé de cache doit distinguer les
// comptes.
//
// Pas de Vary: Cookie pour autant — no-store interdit la conservation, donc la
// clé n'a plus à distinguer quoi que ce soit.
//
// Le prix, assumé : une navigation arrière redemande la page au serveur au lieu
// de la relire dans le cache du navigateur.
const cacheControlDesPages = "private, no-store"

// cheminsSansCacheControl énumère ce qui reste mis en cache, par préfixe.
//
//   - /statique/ : la feuille de style et htmx, servis par http.ServeContent
//     (tools/router/event.go) qui ne pose aucun Cache-Control. Y écrire
//     no-store remplacerait la mise en cache heuristique du navigateur par un
//     rechargement des deux à chaque page.
//   - /api/ : d'où viennent les illustrations, /api/files/…?thumb=… — une
//     vignette par recette de la liste.
//   - /_/ : le panneau d'administration, qui pose son propre max-age, et
//     seulement si le champ est encore vide (apis/serve.go).
var cheminsSansCacheControl = []string{"/statique/", "/api/", "/_/"}

// porteLeCacheControl dit si le chemin demandé reçoit l'en-tête.
//
// Par préfixe, et non route par route : une page ajoutée demain est couverte
// sans que personne n'y pense, alors qu'un oubli sur une route nouvelle ne se
// verrait jamais. La décision tient dans une fonction pure, testable sans
// montage.
func porteLeCacheControl(chemin string) bool {
	for _, prefixe := range cheminsSansCacheControl {
		if strings.HasPrefix(chemin, prefixe) {
			return false
		}
	}
	return true
}

// poseLesEntetesDeReponse ajoute nos en-têtes de sécurité à toute réponse.
//
// Lié par routeur.Bind, il atteint toute réponse sans qu'aucune route ait à
// être énumérée. La priorité par défaut suffit : les middlewares de PocketBase
// portent toutes des priorités négatives (apis/middlewares.go), celui-ci passe
// donc après eux — et un middleware s'exécute de toute façon avant le
// gestionnaire qui écrit le corps, une redirection comprise, qui n'en écrit
// aucun.
//
// Il s'ajoute à pbSecurityHeaders, il ne le remplace pas : les trois en-têtes
// que PocketBase pose restent sur la réponse.
//
// Les deux en-têtes n'ont pas la même portée, et c'est voulu :
//
// Referrer-Policy: no-referrer — sur tout, y compris l'API et le panneau /_/.
// Rien ici ne lit le Referer, ni le serveur ni les pages : le plus strict ne
// coûte rien. Ce qu'il retient est l'adresse de l'instance, que le navigateur
// présenterait autrement au site dont il va chercher l'aperçu de l'image
// importée. Pour une installation auto-hébergée sur un domaine privé, c'est la
// révélation de son existence.
//
// Deux effets assumés : le champ referer du journal d'activité de PocketBase
// devient vide — c'est un champ de diagnostic, l'URL demandée y est déjà —, et
// l'aperçu d'une image distante cesse de s'afficher chez les sites qui
// refusent une requête sans Referer. C'est le prix de la mesure.
//
// Cache-Control: private, no-store — sur nos pages et nos fragments
// seulement, cf. cheminsSansCacheControl : ce qui est public et immuable gagne
// au contraire à rester mis en cache.
func poseLesEntetesDeReponse() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "patachooEntetesDeReponse",
		Func: func(e *core.RequestEvent) error {
			e.Response.Header().Set("Referrer-Policy", "no-referrer")
			if porteLeCacheControl(e.Request.URL.Path) {
				e.Response.Header().Set("Cache-Control", cacheControlDesPages)
			}
			return e.Next()
		},
	}
}
