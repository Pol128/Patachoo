package main

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// poseLesEntetesDeReponse ajoute nos en-têtes de sécurité à toute réponse.
//
// Lié par routeur.Bind, il couvre l'API et le panneau /_/ autant que nos
// pages, sans qu'aucune route ait à être énumérée. La priorité par défaut
// suffit : les middlewares de PocketBase portent toutes des priorités
// négatives (apis/middlewares.go), celui-ci passe donc après eux — et un
// middleware s'exécute de toute façon avant le gestionnaire qui écrit le
// corps.
//
// Il s'ajoute à pbSecurityHeaders, il ne le remplace pas : les trois en-têtes
// que PocketBase pose restent sur la réponse.
//
// Referrer-Policy: no-referrer — et non same-origin ou le défaut des
// navigateurs. Rien ici ne lit le Referer, ni le serveur ni les pages : le
// plus strict ne coûte rien. Ce qu'il retient est l'adresse de l'instance,
// que le navigateur présenterait autrement au site dont il va chercher
// l'aperçu de l'image importée. Pour une installation auto-hébergée sur un
// domaine privé, c'est la révélation de son existence.
//
// Deux effets assumés : le champ referer du journal d'activité de PocketBase
// devient vide — c'est un champ de diagnostic, l'URL demandée y est déjà —, et
// l'aperçu d'une image distante cesse de s'afficher chez les sites qui
// refusent une requête sans Referer. C'est le prix de la mesure.
func poseLesEntetesDeReponse() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "patachooEntetesDeReponse",
		Func: func(e *core.RequestEvent) error {
			e.Response.Header().Set("Referrer-Policy", "no-referrer")
			return e.Next()
		},
	}
}
