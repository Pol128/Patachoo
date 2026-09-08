package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// parPage : le nombre de vignettes d'une page. Pagination classique et non
// défilement progressif — une URL partageable, une page imprimable, et des
// critères qui tiennent tous dans la chaîne de requête.
const parPage = 24

// pageMax borne le numéro de page accepté.
//
// Ce n'est pas un confort d'affichage : le décalage se calcule par
// (page-1)*parPage, et un « ?page=999999999999999999 » le ferait déborder en
// négatif — que FindRecordsByFilter ignore, rendant alors la première page à
// qui a demandé la dernière. Une borne bien au-delà de tout carnet réel.
const pageMax = 1 << 20

// filtreDeRecherche restreint aux recettes dont le titre, l'une des lignes
// d'ingrédient ou l'un des noms de tag contient le terme.
//
// « ?~ » et non « ~ » sur les deux chemins multi-valués : sur une relation
// multiple ou inverse, « ~ » exige que *toutes* les valeurs correspondent, et
// « tags.name ~ {:q} » ne ramènerait qu'une recette dont chaque tag contient le
// terme. L'opérateur « au moins une » est « ?~ ».
const filtreDeRecherche = "title ~ {:q} || ingredients_via_recipe.raw ?~ {:q} || tags.name ?~ {:q}"

// criteres porte ce que la chaîne de requête dit de la liste.
//
// Un seul type et un seul lecteur : les filtres par tag, par type de plat et
// par saison (PATA-16, PATA-17, PATA-18) s'y ajouteront sans que la liste
// n'ait à être réécrite.
type criteres struct {
	Terme string
	Page  int
}

// lisLesCriteres est le seul endroit où la chaîne de requête est lue.
//
// Un paramètre malmené ne produit pas d'erreur : une page non numérique, nulle
// ou négative retombe sur la première. Refuser vaudrait une 500 pour un lien
// mal recopié.
func lisLesCriteres(r *http.Request) criteres {
	requete := r.URL.Query()

	page, err := strconv.Atoi(requete.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > pageMax {
		page = pageMax
	}

	return criteres{
		Terme: strings.TrimSpace(requete.Get("q")),
		Page:  page,
	}
}

// lien rend l'URL de la liste pour ces critères, page comprise.
//
// La page 1 et un terme vide ne s'écrivent pas : « /recettes » et
// « /recettes?page=1 » désigneraient la même chose sous deux adresses.
func (c criteres) lien(page int) string {
	valeurs := url.Values{}
	if c.Terme != "" {
		valeurs.Set("q", c.Terme)
	}
	if page > 1 {
		valeurs.Set("page", strconv.Itoa(page))
	}

	if len(valeurs) == 0 {
		return "/recettes"
	}
	return "/recettes?" + valeurs.Encode()
}

// vignette : ce qu'une case de la grille affiche, et rien de plus.
//
// Les champs facultatifs sont vides quand la donnée manque, et le gabarit
// n'écrit alors rien : ni cadre vide, ni libellé orphelin.
type vignette struct {
	Id         string
	Titre      string
	Miniature  string
	TypeDePlat string
	Tags       []string
}

// donneesRecettes est ce que la page de liste donne à ses gabarits.
type donneesRecettes struct {
	donneesPage
	Terme      string
	Vignettes  []vignette
	CarnetVide bool
	Precedente string
	Suivante   string
}

// pageListeRecettes rend la liste des recettes : la page d'accueil de l'outil,
// et la cible des liens de toutes les autres pages.
//
// La route vérifie la session elle-même : servie par notre code Go, elle est
// hors des règles de collection, qui ne gardent que l'API REST. Sans session,
// le carnet n'est pas rendu et la requête part vers la page de connexion.
func pageListeRecettes(e *core.RequestEvent) error {
	if e.Auth == nil {
		return e.Redirect(http.StatusFound, "/connexion")
	}

	criteres := lisLesCriteres(e.Request)

	// Une vignette de plus que la page : c'est ce dépassement, et lui seul, qui
	// dit qu'il existe un rang suivant. CountRecords n'accepte que des
	// dbx.Expression, pas un filtre en chaîne, et reconstruire l'expression
	// pour un simple « y en a-t-il d'autres » coûterait plus que la ligne lue
	// en trop.
	trouvees, err := recettesDuRang(e.App, criteres)
	if err != nil {
		return err
	}

	suivante := len(trouvees) > parPage
	if suivante {
		trouvees = trouvees[:parPage]
	}

	vignettes, err := vignettesDe(e.App, trouvees)
	if err != nil {
		return err
	}

	donnees := donneesRecettes{
		donneesPage: donneesPage{Titre: "Recettes — Patachoo"},
		Terme:       criteres.Terme,
		Vignettes:   vignettes,
		// Le carnet vide se distingue de la recherche sans résultat : les
		// confondre afficherait « votre carnet est vide » à quelqu'un qui a
		// simplement mal orthographié un mot. Il se déduit sans compter :
		// la première page, sans terme, ne peut être vide que si le carnet
		// l'est.
		CarnetVide: len(vignettes) == 0 && criteres.Terme == "" && criteres.Page == 1,
	}
	if criteres.Page > 1 {
		donnees.Precedente = criteres.lien(criteres.Page - 1)
	}
	if suivante {
		donnees.Suivante = criteres.lien(criteres.Page + 1)
	}

	return rendre(e, "recettes.html", "recettes-resultats.html", &donnees)
}

// recettesDuRang lit une page de recettes, une de plus que nécessaire.
//
// FindRecordsByFilter et non une requête écrite à la main : le filtre en
// chaîne résout seul la relation inverse des ingrédients et la relation
// multiple des tags, et il déduplique les lignes que ces jointures
// multiplieraient.
func recettesDuRang(app core.App, criteres criteres) ([]*core.Record, error) {
	filtre := ""
	params := dbx.Params{}
	if criteres.Terme != "" {
		filtre = filtreDeRecherche
		params["q"] = termeCherchable(criteres.Terme)
	}

	return app.FindRecordsByFilter(
		"recipes",
		filtre,
		"-created",
		parPage+1,
		(criteres.Page-1)*parPage,
		params,
	)
}

// termeCherchable neutralise les jokers de LIKE dans un terme saisi.
//
// PocketBase échappe bien « % » et « _ » dans une valeur liée — mais seulement
// si elle ne porte aucun « % » non échappé (tools/search/filter.go,
// wrapLikeParams) : une valeur qui en contient un est laissée telle quelle, et
// une recherche sur « % » ramènerait tout le carnet. En échappant nous-mêmes,
// la valeur n'entre jamais dans ce cas — et l'échappement de PocketBase, qui
// respecte ce qui l'est déjà, la traverse sans la doubler.
func termeCherchable(terme string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(terme)
}

// vignettesDe traduit les enregistrements en ce que la grille affiche.
func vignettesDe(app core.App, recettes []*core.Record) ([]vignette, error) {
	if len(recettes) == 0 {
		return nil, nil
	}

	if echecs := app.ExpandRecords(recettes, []string{"tags", "meal_type"}, nil); len(echecs) > 0 {
		return nil, fmt.Errorf("relations des recettes : %v", echecs)
	}

	vignettes := make([]vignette, 0, len(recettes))
	for _, recette := range recettes {
		vignettes = append(vignettes, vignette{
			Id:         recette.Id,
			Titre:      recette.GetString("title"),
			Miniature:  miniature(recette),
			TypeDePlat: nomDe(recette.ExpandedOne("meal_type")),
			Tags:       nomsDe(recette.ExpandedAll("tags")),
		})
	}
	return vignettes, nil
}

// miniature rend l'URL de la vignette 300x200, ou "" si aucune image n'est
// stockée — auquel cas le gabarit n'écrit pas d'image du tout, plutôt qu'une
// image cassée.
func miniature(recette *core.Record) string {
	fichier := recette.GetString("image")
	if fichier == "" {
		return ""
	}
	return "/api/files/recipes/" + recette.Id + "/" + url.PathEscape(fichier) + "?thumb=300x200"
}

func nomDe(enregistrement *core.Record) string {
	if enregistrement == nil {
		return ""
	}
	return enregistrement.GetString("name")
}

func nomsDe(enregistrements []*core.Record) []string {
	if len(enregistrements) == 0 {
		return nil
	}

	noms := make([]string, 0, len(enregistrements))
	for _, enregistrement := range enregistrements {
		noms = append(noms, enregistrement.GetString("name"))
	}
	return noms
}
