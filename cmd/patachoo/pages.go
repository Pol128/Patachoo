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

	// Version n'est pas remplie par les pages non plus, et pour la même
	// raison : c'est rendre qui la pose. Le pied de page ne l'affiche qu'à un
	// compte connecté — c'est le gabarit qui tient cette règle, en testant
	// Utilisateur.
	Version string

	// JetonAntiRejeu est la moitié de la paire que chaque formulaire en POST
	// recopie dans un champ caché. Posée par rendre, comme les deux
	// précédentes, et jamais par les pages : une page qui oublierait de la
	// recopier verrait sa propre soumission refusée en 403, et le formulaire
	// serait mort sans que rien ne le dise à sa route.
	JetonAntiRejeu string

	// Recette n'est rempli que par la fiche, et nil partout ailleurs : le
	// gabarit de la fiche s'ouvre sur un {{with}}, donc une page qui l'oublie
	// ne rend rien plutôt que d'échouer à mi-parcours.
	Recette *donneesRecette

	// Commentaires porte le bloc des notes. Nil pour toute page qui n'en
	// affiche pas — le fragment s'ouvre sur un {{with}} comme la fiche —, et
	// c'est aussi lui que les quatre routes de PATA-22 renvoient seul à HTMX.
	Commentaires *donneesCommentaires

	// Formulaire n'est rempli que par les pages qui en portent un. Un champ
	// par page plutôt qu'un any : le gabarit nomme ce qu'il lit, et une page
	// qui se tromperait de forme rougirait au rendu plutôt qu'en production.
	Formulaire *formulaireRecette
}

// poseUtilisateur implémente donneesDePage. Sur donneesPage, donc valable pour
// toute structure de page qui l'embarque : c'est ce qui permet à une page de
// porter ses propres champs sans que rendre cesse de remplir celui-ci.
func (d *donneesPage) poseUtilisateur(compte *utilisateur) {
	d.Utilisateur = compte
}

// poseVersion, de même : la version du binaire est une donnée de la mise en
// page, pas de la page.
func (d *donneesPage) poseVersion(v string) {
	d.Version = v
}

// poseJetonAntiRejeu, de même : le jeton est une donnée de la requête, pas de
// la page.
func (d *donneesPage) poseJetonAntiRejeu(jeton string) {
	d.JetonAntiRejeu = jeton
}

// donneesDePage est ce que rendre sait remplir : n'importe quelle structure de
// page, pourvu qu'elle embarque donneesPage. Un pointeur, toujours — une copie
// recevrait l'utilisateur et le gabarit lirait l'original.
type donneesDePage interface {
	poseUtilisateur(*utilisateur)
	poseVersion(string)
	poseJetonAntiRejeu(string)
}

// pageAccueil renvoie à la liste des recettes.
//
// Une seule URL canonique pour le carnet, et c'est /recettes : deux entrées
// rendant la même page se mettraient à diverger, et c'est /recettes que les
// pages suivantes pointent.
func pageAccueil(e *core.RequestEvent) error {
	return e.Redirect(http.StatusFound, "/recettes")
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
//   - une page faite de plusieurs blocs nomme les autres dans enPlus, sinon le
//     gabarit qui les inclut ne les trouve pas. L'ordre y est indifférent :
//     seul le premier motif décide du gabarit exécuté, et c'est ce qui permet
//     à une route de rendre tantôt la fiche entière, tantôt le seul bloc des
//     notes, avec le même jeu de fichiers.
//
// Le rendu passe par un tampon avant d'être écrit : une erreur de gabarit
// remonte comme erreur et ne peut pas produire une demi-page déjà partie sur
// le réseau.
func rendre(e *core.RequestEvent, page, fragment string, donnees donneesDePage, enPlus ...string) error {
	return rendreAvecStatut(e, http.StatusOK, page, fragment, donnees, enPlus...)
}

// rendreAvecStatut rend la même chose sous un autre code de retour.
//
// Une page introuvable est une page comme une autre — mise en page, en-tête du
// compte, feuille de style — et seul son statut la distingue. Un 404 rendu par
// rendre() répondrait 200, et un navigateur comme un moteur d'indexation
// prendraient l'erreur pour une page valide.
func rendreAvecStatut(e *core.RequestEvent, statut int, page, fragment string, donnees donneesDePage, enPlus ...string) error {
	donnees.poseUtilisateur(utilisateurCourant(e))
	donnees.poseVersion(versionAffichee)
	donnees.poseJetonAntiRejeu(jetonAntiRejeuCourant(e))

	motifs := []string{"vues/" + fragment}
	if !estHTMX(e) {
		motifs = []string{"vues/mise-en-page.html", "vues/" + page, "vues/" + fragment}
	}
	for _, gabarit := range enPlus {
		motifs = append(motifs, "vues/"+gabarit)
	}

	rendu, err := registre.LoadFS(vues, motifs...).Render(donnees)
	if err != nil {
		return err
	}

	return e.HTML(statut, rendu)
}

// rendLeBlocSeul écrit un bloc seul, quelle que soit l'origine de la requête.
//
// Une route qui n'a pas de page complète à proposer — le champ de tags et ses
// suggestions n'en forment pas une — n'a pas non plus d'arbitrage à faire :
// elle rend son fichier, et rien autour. Ses données ne sont pas celles d'une
// page, et ne portent donc pas l'utilisateur courant.
//
// enPlus nomme les gabarits que le bloc inclut, comme rendre le fait pour une
// page. Ils sont chargés même quand la branche qui les appelle n'est pas prise :
// html/template parcourt tout l'arbre pour poser son échappement, et refuse un
// {{template}} dont le fichier manque, fût-il dans un {{if}} faux.
func rendLeBlocSeul(e *core.RequestEvent, bloc string, donnees any, enPlus ...string) error {
	motifs := []string{"vues/" + bloc}
	for _, gabarit := range enPlus {
		motifs = append(motifs, "vues/"+gabarit)
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
