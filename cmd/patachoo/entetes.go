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

// politiqueDeContenu est la politique de sécurité du contenu posée sur nos
// pages. Écrite en dur, et non exposée en réglage : une politique qu'on peut
// desserrer par variable d'environnement finit desserrée.
//
// Le produit importe du contenu tiers par construction — sa surface
// d'injection est sa fonction, pas un accident. Cette politique est le cran qui
// manque entre une erreur d'échappement et la prise du compte par l'API : un
// script injecté dans une page ne s'exécute plus, il s'écrit dans la console.
//
// Deux directives méritent leur justification :
//
//   - img-src sort de 'self' : le formulaire d'import affiche l'aperçu de
//     l'image proposée par le site importé, chargée depuis ce site. http: et
//     https: sont larges, et c'est assumé — la seule alternative étroite est de
//     faire passer l'aperçu par notre serveur, ce qui ouvre un proxy d'image.
//   - frame-ancestors 'none' est plus strict que le X-Frame-Options: SAMEORIGIN
//     de PocketBase, et l'emporte sur lui dans les navigateurs modernes : plus
//     aucune page de Patachoo ne s'affiche en cadre. Rien ici n'en emploie.
//
// Ni 'unsafe-inline' ni 'unsafe-eval' : aucun gabarit ne porte de style ni de
// gestionnaire en ligne, et la meta htmx-config de mise-en-page.html ferme les
// deux chemins d'exécution de htmx. Un test balaie les gabarits pour que cela
// reste vrai.
const politiqueDeContenu = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' http: https:; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// Les deux préfixes que notre politique laisse tranquilles.
//
// PocketBase pose les siennes « seulement si l'en-tête est absent », et un
// middleware lié au routeur s'exécute avant elles : poser la nôtre partout ne
// s'ajouterait pas à la leur, elle la remplacerait. Le panneau
// d'administration recevrait default-src 'none' et ne chargerait plus rien ;
// les fichiers servis perdraient leur politique sandbox, ce qui est un recul.
const (
	prefixePanneau = "/_/"
	prefixeAPI     = "/api/"
)

// laPolitiqueSApplique dit si un chemin reçoit notre politique.
//
// Sortie du middleware pour être vérifiable seule : la route du panneau
// d'administration est enregistrée dans un hook OnServe qu'un test ne déclenche
// pas, et la règle qui la protège ne se lirait donc dans aucune réponse.
//
// /statique/ la reçoit comme le reste : elle n'y a aucun effet, et une
// exception de plus serait une exception à maintenir. C'est ce qui la distingue
// de porteLeCacheControl, qui exclut /statique/ : les deux portées se
// ressemblent sans se confondre, et chacune a sa fonction.
func laPolitiqueSApplique(chemin string) bool {
	return !strings.HasPrefix(chemin, prefixePanneau) && !strings.HasPrefix(chemin, prefixeAPI)
}

// prioriteDesEntetesDeReponse fait passer nos en-têtes avant nos propres
// couches, et non plus seulement avant les gestionnaires.
//
// La priorité par défaut a suffi tant qu'aucune couche à nous n'en déclarait :
// les middlewares de PocketBase portent toutes des priorités négatives
// (apis/middlewares.go), celle-ci passait donc après elles, et un middleware
// s'exécute de toute façon avant le gestionnaire qui écrit le corps — une
// redirection comprise, qui n'en écrit aucun.
//
// Elle ne suffit plus depuis que la garde de l'établi porte la sienne. Le
// crochet est trié par priorité croissante (tools/hook/hook.go), et une couche
// qui rend sa réponse sans appeler e.Next() court-circuite tout ce qui la suit :
// une couche à nous, passée avant celle-ci, rendrait ses refus sans nos
// en-têtes. Un cran avant la première d'entre elles, donc, et lu sur sa
// constante — le jour où elle bouge, celle-ci suit.
//
// Reste très en aval des en-têtes de PocketBase
// (DefaultSecurityHeadersMiddlewarePriority, -1010), qui passent donc toujours
// avant : les nôtres s'ajoutent aux leurs, elles ne les remplacent pas.
const prioriteDesEntetesDeReponse = prioriteDuDroitDeCurateur - 1

// poseLesEntetesDeReponse ajoute nos en-têtes de sécurité à toute réponse.
//
// Lié par routeur.Bind, il atteint toute réponse sans qu'aucune route ait à
// être énumérée, et sa priorité le fait passer avant celles de nos couches qui
// rendent sans passer la main.
//
// Il s'ajoute à pbSecurityHeaders, il ne le remplace pas : les trois en-têtes
// que PocketBase pose restent sur la réponse.
//
// Les trois en-têtes n'ont pas la même portée, et c'est voulu :
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
//
// Content-Security-Policy — sur nos pages seulement, cf. laPolitiqueSApplique :
// contrairement aux deux autres, PocketBase en pose déjà pour le panneau et
// pour les fichiers servis, et l'écraser serait un recul.
func poseLesEntetesDeReponse() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooEntetesDeReponse",
		Priority: prioriteDesEntetesDeReponse,
		Func: func(e *core.RequestEvent) error {
			e.Response.Header().Set("Referrer-Policy", "no-referrer")
			if porteLeCacheControl(e.Request.URL.Path) {
				e.Response.Header().Set("Cache-Control", cacheControlDesPages)
			}
			if laPolitiqueSApplique(e.Request.URL.Path) {
				e.Response.Header().Set("Content-Security-Policy", politiqueDeContenu)
			}
			return e.Next()
		},
	}
}
