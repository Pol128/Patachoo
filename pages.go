package main

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/template"
)

// Gabarits et assets partent dans le binaire dès maintenant : c'est plus
// pénible à rattraper après coup que d'être fait dès le départ. Un go build
// suffit donc toujours — ni Node, ni node_modules, ni étape de construction.
//
//go:embed vues/*.html
var vues embed.FS

//go:embed statique
var statique embed.FS

// registre est créé une fois et partagé par toutes les pages : il est sûr en
// accès concurrent et met en cache le jeu de fichiers parsé.
var registre = template.NewRegistry()

// donneesPage porte ce que la mise en page et le contenu ont en commun.
//
// Utilisateur n'est pas rempli par les pages : c'est rendre qui le pose depuis
// e.Auth. Une page qui oublierait de le recopier afficherait un en-tête de
// visiteur à un compte connecté, et personne ne s'en apercevrait avant la mise
// en ligne.
type donneesPage struct {
	Titre       string
	Message     string
	Utilisateur *utilisateur

	// Formulaire n'est rempli que par les pages qui en portent un. Un champ
	// par page plutôt qu'un any : le gabarit nomme ce qu'il lit, et une page
	// qui se tromperait de forme rougirait au rendu plutôt qu'en production.
	Formulaire *formulaireRecette
}

// pageAccueil sert la page d'accueil.
//
// Elle garde son message d'attente — la liste des recettes est PATA-13 —, mais
// il passe désormais par les gabarits, et elle exerce les deux chemins de
// rendu : une convention qu'aucune page n'emprunte n'est pas une convention.
func pageAccueil(e *core.RequestEvent) error {
	return rendre(e, "accueil.html", "accueil-corps.html", donneesPage{
		Titre:   "Patachoo",
		Message: "Le carnet de recettes est en construction.",
	})
}

// rendre écrit soit le document complet, soit le seul fragment.
//
// C'est ici, et nulle part ailleurs, que se fait le choix entre les deux :
// chaque route qui répond à HTMX rend un fragment, jamais la page entière —
// une page complète renvoyée dans un hx-target produit des pages imbriquées.
//
// La convention que les pages suivantes reprennent :
//
//   - la mise en page passe toujours en premier à LoadFS, parce que Render
//     exécute le gabarit nommé d'après le premier fichier du premier motif ;
//   - un gabarit de page définit {{define "contenu"}} et n'est jamais premier ;
//   - un bloc qu'une route peut renvoyer seul vit dans son propre fichier, au
//     niveau racine, sans {{define}} autour. Chargé seul, un fichier réduit à
//     un {{define}} rendrait une chaîne vide sans la moindre erreur.
//
// Le rendu passe par un tampon avant d'être écrit : une erreur de gabarit
// remonte comme erreur et ne peut pas produire une demi-page déjà partie sur
// le réseau.
func rendre(e *core.RequestEvent, page, fragment string, donnees donneesPage) error {
	donnees.Utilisateur = utilisateurCourant(e)

	motifs := []string{"vues/" + fragment}
	if !estHTMX(e) {
		motifs = []string{"vues/mise-en-page.html", "vues/" + page, "vues/" + fragment}
	}

	rendu, err := registre.LoadFS(vues, motifs...).Render(donnees)
	if err != nil {
		return err
	}

	return e.HTML(http.StatusOK, rendu)
}

// estHTMX dit si la requête vient de HTMX, qui se signale par un en-tête.
func estHTMX(e *core.RequestEvent) bool {
	return e.Request.Header.Get("HX-Request") == "true"
}

// assetsStatiques sert la feuille CSS et HTMX depuis nos propres fichiers.
func assetsStatiques() func(*core.RequestEvent) error {
	return servirAssets(apis.MustSubFS(statique, "statique"))
}

// servirAssets sert un système de fichiers d'assets, et rien d'autre.
//
// Le false passé à Static est délibéré : un asset absent doit être un 404, pas
// la page d'accueil déguisée en fichier JavaScript. La fonction prend son
// système de fichiers en paramètre pour que ce choix soit vérifiable — servi
// sur nos seuls assets, il ne se distingue pas de son contraire.
func servirAssets(fsys fs.FS) func(*core.RequestEvent) error {
	return apis.Static(fsys, false)
}
