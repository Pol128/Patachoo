package main

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

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
// exception de plus serait une exception à maintenir.
func laPolitiqueSApplique(chemin string) bool {
	return !strings.HasPrefix(chemin, prefixePanneau) && !strings.HasPrefix(chemin, prefixeAPI)
}

// poseLesEntetesDeReponse pose le middleware d'en-têtes sur le routeur, comme
// brancheLaSession pose les siens : lié une fois, il vaut pour toutes les
// routes, et aucune n'est énumérée.
func poseLesEntetesDeReponse(routeur *router.Router[*core.RequestEvent]) {
	routeur.Bind(entetesDeReponse())
}

// entetesDeReponse écrit nos en-têtes avant que le gestionnaire ne rende le
// corps — un en-tête posé après le corps ne partirait pas.
//
// La priorité par défaut suffit : les middlewares de PocketBase portent des
// priorités négatives, et un middleware s'exécute de toute façon avant le
// gestionnaire.
func entetesDeReponse() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "patachooEntetesDeReponse",
		Func: func(e *core.RequestEvent) error {
			if laPolitiqueSApplique(e.Request.URL.Path) {
				e.Response.Header().Set("Content-Security-Policy", politiqueDeContenu)
			}
			return e.Next()
		},
	}
}
