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

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func main() {
	app := pocketbase.New()

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/", pageAccueil)
		se.Router.POST("/api/import", importDepuisURL)

		// se.Next() laisse la main aux routes de PocketBase : sans lui,
		// l'interface d'administration et l'API REST ne répondent plus.
		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
