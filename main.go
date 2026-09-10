// Commande patachoo : le serveur.
//
// PocketBase est utilisé ici comme bibliothèque Go, pas comme exécutable tout
// fait. C'est ce qui permet d'ajouter nos propres routes — import, rendu des
// pages — et de tout livrer dans un binaire unique.
//
// Fourni par PocketBase, et donc jamais réécrit ici : SQLite, migrations, API
// REST, authentification, stockage des fichiers, règles d'accès et interface
// d'administration sur /_/.
package main

import (
	"log"
	"os"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/pocketbase/pocketbase/tools/router"

	// Le schéma vit dans le dépôt : l'importer suffit à l'enregistrer.
	_ "github.com/Pol128/Patachoo/migrations"
)

func main() {
	// La sonde du HEALTHCHECK, traitée avant tout le reste. Enregistrée sur
	// app.RootCmd, elle serait une commande connue de PocketBase, et
	// app.Start() amorcerait l'application entière avant de la lancer : data.db
	// et auxiliary.db ouvertes puis jamais refermées, migrations système
	// jouées, et pb_data/.pb_temp_to_delete effacé — le répertoire de travail
	// d'une sauvegarde ou d'une restauration en cours. Toutes les trente
	// secondes, sur le volume vivant, pour un GET sur la boucle locale.
	if santeDemandee(os.Args[1:]) {
		os.Exit(lanceSante(os.Args[1:], os.Stderr))
	}

	app := pocketbase.New()

	// Automigrate à false : les migrations s'écrivent à la main et se relisent
	// en revue. Une migration générée par une manipulation dans l'interface
	// d'administration décrirait un schéma que personne n'a décidé.
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{Automigrate: false})

	// Le pack de langue et le lexique d'aliments sont lus ici, une fois, et
	// nulle part ailleurs : les recharger sur le chemin d'une ligne mettrait
	// le coût du chargement sur chaque ingrédient importé.
	analyseur, err := analyseurFR()
	if err != nil {
		log.Fatal(err)
	}

	// Sur l'app, pas dans OnServe : les règles de modèle valent aussi pour un
	// tag créé par une commande, où le serveur ne tourne pas.
	brancheLesHooks(app, analyseur)

	// L'ouvrier de l'import en lot, lui, ne vit que le temps du service : il
	// sort sur le réseau, et une commande qui migre ou qui fusionne n'a rien à
	// faire partir. Il s'accroche donc à OnServe et à OnTerminate, qu'il pose
	// lui-même.
	brancheLOuvrier(app)

	// Avant app.Start() : c'est Execute() qui amorce l'application puis exécute
	// la sous-commande demandée, laquelle dispose donc d'une base ouverte.
	commandeAEchoue := brancheLesCommandes(app, app.RootCmd)

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		brancheLesRoutes(se.Router)

		// se.Next() laisse la main aux routes de PocketBase : sans lui,
		// l'interface d'administration et l'API REST ne répondent plus.
		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}

	// PocketBase avale l'erreur rendue par la sous-commande : sans ce témoin,
	// une fusion refusée sortirait sur zéro et passerait pour réussie.
	if commandeAEchoue() {
		os.Exit(1)
	}
}

// brancheLesRoutes pose nos middlewares et nos routes sur le routeur.
//
// À part de OnServe pour être montable dans un test : c'est l'ordre des
// middlewares qui fait tenir la session, et un test qui rebâtirait son propre
// montage ne vérifierait que lui-même.
func brancheLesRoutes(routeur *router.Router[*core.RequestEvent]) {
	brancheLaSession(routeur)

	routeur.GET("/", pageAccueil)
	routeur.GET("/recettes", pageListeRecettes)
	routeur.GET("/recettes/{id}", pageRecette)
	routeur.GET("/connexion", pageConnexion)
	routeur.POST("/connexion", connexion).Bind(rendLeDepassementEnHTML())
	routeur.POST("/deconnexion", deconnexion)
	routeur.GET("/inscription", pageInscription)
	routeur.POST("/inscription", inscription)
	brancheLesRecettes(routeur)
	brancheLesTags(routeur)
	brancheLesCommentaires(routeur)
	brancheLImport(routeur)
	brancheLImportEnLot(routeur)

	// Nos propres assets, embarqués dans le binaire : ni CDN, ni domaine
	// tiers. Patachoo doit fonctionner sur un réseau coupé d'Internet.
	routeur.GET("/statique/{path...}", assetsStatiques())
}
