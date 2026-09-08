package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// miniatureDeLaFiche : la fiche affiche la vignette déclarée par le schéma, pas
// le fichier d'origine. Une photo de recette pèse volontiers deux mégaoctets, et
// c'est la page qu'on ouvre les mains dans la farine.
const miniatureDeLaFiche = "800x0"

// donneesRecette porte la fiche telle que le gabarit la lit.
//
// Tout y est déjà mis en forme — durées, quantités, listes de noms — parce que
// le gabarit n'a pas de quoi le faire : il affiche ou il n'affiche pas. Un
// champ vide y signifie donc « ce bloc n'existe pas », et c'est ce qui fait
// disparaître les libellés des données manquantes.
type donneesRecette struct {
	Titre       string
	Image       string
	Faits       []fait
	Source      *source
	Ingredients []ligneDIngredient
	Etapes      []string
}

// fait est un couple libellé/valeur du bandeau : portions, durées, type de
// plat, saisons, tags, auteur. Une liste plutôt qu'un champ par donnée : le
// bandeau entier disparaît quand elle est vide, sans conteneur laissé vide.
type fait struct {
	Libelle string
	Valeur  string
}

// source est le site d'où la recette vient. PATA-11 reprend ce bloc à fond ;
// ici, le lien existe et fonctionne.
type source struct {
	URL string
	Nom string
}

// ligneDIngredient porte les deux modes d'affichage d'une ligne.
//
// Brut est toujours renseigné — c'est le seul champ requis par le schéma — et
// sert de repli quand Aliment est vide. Le gabarit tranche sur Aliment, et rien
// d'autre : le schéma ne porte aucun indice de confiance, l'absence d'aliment
// reconnu est la seule traduction implémentable de « vide ou douteux ».
type ligneDIngredient struct {
	Brut       string
	Quantite   string
	Unite      string
	Aliment    string
	Note       string
	Facultatif bool
}

// pageRecette sert la fiche d'une recette.
//
// Le contrôle de session passe avant la recherche de l'enregistrement : sinon
// la différence entre un 404 et une redirection dirait à un visiteur quels
// identifiants existent. Cette route étant servie par notre code, les règles de
// collection ne la gardent pas — c'est à elle de vérifier la session.
func pageRecette(e *core.RequestEvent) error {
	if e.Auth == nil {
		// La page de connexion de PATA-36, telle que main() la déclare et que
		// la mise en page la propose déjà au visiteur.
		return e.Redirect(http.StatusSeeOther, "/connexion")
	}

	recette, err := e.App.FindRecordById("recipes", e.Request.PathValue("id"))
	if err != nil {
		// Seul l'identifiant inconnu devient un 404 : une base injoignable
		// remonte comme erreur, sinon la page annoncerait une recette
		// supprimée à chaque incident.
		if errors.Is(err, sql.ErrNoRows) {
			return pageRecetteIntrouvable(e)
		}
		return err
	}

	donnees, err := ficheDeLaRecette(e.App, recette)
	if err != nil {
		return err
	}

	// Les notes sont un bloc de la fiche : elles se lisent avec elle, et les
	// quatre routes de PATA-22 renvoient ce même bloc seul.
	notes, err := blocDesNotes(e, recette, "")
	if err != nil {
		return err
	}

	return rendre(e, "recette.html", "recette-corps.html", &donneesPage{
		Titre:        recette.GetString("title") + " — Patachoo",
		Recette:      donnees,
		Commentaires: notes,
	}, "commentaires.html")
}

// pageRecetteIntrouvable répond par une page lisible, et non par une 500 ni une
// page vide.
func pageRecetteIntrouvable(e *core.RequestEvent) error {
	return rendreAvecStatut(e, http.StatusNotFound,
		"recette-introuvable.html", "recette-introuvable-corps.html", &donneesPage{
			Titre: "Recette introuvable — Patachoo",
		})
}

// ficheDeLaRecette met la recette et ses ingrédients en forme pour le gabarit.
func ficheDeLaRecette(app core.App, recette *core.Record) (*donneesRecette, error) {
	lignes, err := app.FindRecordsByFilter(
		"ingredients",
		"recipe = {:recette}",
		"position",
		0,
		0,
		dbx.Params{"recette": recette.Id},
	)
	if err != nil {
		return nil, fmt.Errorf("ingrédients de %s : %w", recette.Id, err)
	}

	if echecs := app.ExpandRecord(recette, []string{"meal_type", "tags", champAuteur}, nil); len(echecs) > 0 {
		return nil, fmt.Errorf("relations de %s : %v", recette.Id, echecs)
	}

	donnees := &donneesRecette{
		Titre:       recette.GetString("title"),
		Image:       urlDeLaMiniature(recette),
		Faits:       faitsDeLaRecette(recette),
		Source:      sourceDeLaRecette(recette),
		Ingredients: ingredientsDeLaRecette(lignes),
		Etapes:      etapes(recette.GetString("instructions")),
	}
	return donnees, nil
}

// faitsDeLaRecette rassemble le bandeau, dans l'ordre où il se lit. Un fait
// dont la valeur est vide n'entre pas dans la liste : c'est ainsi que le
// libellé disparaît avec la donnée.
func faitsDeLaRecette(recette *core.Record) []fait {
	var faits []fait
	ajoute := func(libelle, valeur string) {
		if valeur != "" {
			faits = append(faits, fait{Libelle: libelle, Valeur: valeur})
		}
	}

	if portions := recette.GetInt("servings"); portions > 0 {
		ajoute("Portions", strconv.Itoa(portions))
	}
	ajoute("Temps de préparation", dureeLisible(recette.GetInt("prep_time")))
	ajoute("Temps de cuisson", dureeLisible(recette.GetInt("cook_time")))

	if typeDePlat := recette.ExpandedOne("meal_type"); typeDePlat != nil {
		ajoute("Type de plat", typeDePlat.GetString("name"))
	}
	// seasons est un select, pas une relation : sa valeur se lit directement,
	// sans passer par l'expansion.
	ajoute("Saisons", strings.Join(recette.GetStringSlice("seasons"), ", "))
	ajoute("Tags", strings.Join(nomsDe(recette.ExpandedAll("tags")), ", "))

	// Le nom, et rien d'autre : l'auteur d'une recette est un autre compte que
	// son lecteur, et son courriel ne lui appartient pas. PocketBase le protège
	// partout ailleurs — users porte ViewRule = id = @request.auth.id et
	// emailVisibility vaut faux —, mais l'expansion de created_by passe à côté
	// de la règle. Faute de nom, le bloc disparaît, comme toute donnée
	// manquante.
	if auteur := recette.ExpandedOne(champAuteur); auteur != nil {
		ajoute("Ajoutée par", strings.TrimSpace(auteur.GetString("name")))
	}

	return faits
}

// sourceDeLaRecette rend le bloc de source, ou nil : les deux champs sont vides
// sur une saisie manuelle, et c'est le cas normal.
func sourceDeLaRecette(recette *core.Record) *source {
	adresse := recette.GetString("source_url")
	nom := recette.GetString("source_name")
	if adresse == "" || nom == "" {
		return nil
	}
	return &source{URL: adresse, Nom: nom}
}

// ingredientsDeLaRecette met chaque ligne en forme, dans l'ordre reçu.
func ingredientsDeLaRecette(lignes []*core.Record) []ligneDIngredient {
	rendues := make([]ligneDIngredient, 0, len(lignes))
	for _, ligne := range lignes {
		rendues = append(rendues, ligneDIngredient{
			Brut:       ligne.GetString("raw"),
			Quantite:   quantiteLisible(ligne.GetFloat("quantity")),
			Unite:      strings.TrimSpace(ligne.GetString("unit")),
			Aliment:    strings.TrimSpace(ligne.GetString("food")),
			Note:       strings.TrimSpace(ligne.GetString("note")),
			Facultatif: ligne.GetBool("optional"),
		})
	}
	return rendues
}

// urlDeLaMiniature rend l'adresse de la vignette, ou "" faute d'image.
//
// Le nom de la collection plutôt que son identifiant : PocketBase accepte les
// deux, et l'adresse reste lisible dans une page comme dans un journal.
func urlDeLaMiniature(recette *core.Record) string {
	fichier := recette.GetString("image")
	if fichier == "" {
		return ""
	}
	return fmt.Sprintf("/api/files/%s/%s/%s?thumb=%s",
		recette.Collection().Name, recette.Id, url.PathEscape(fichier), miniatureDeLaFiche)
}

// etapes découpe les instructions en une étape par ligne non vide.
//
// instructions porte du texte, pas du HTML — tranché le 19/08/2026 : le gabarit
// l'échappe, et les lignes vides séparent sans produire d'étape vide.
func etapes(instructions string) []string {
	var decoupees []string
	for _, ligne := range strings.Split(instructions, "\n") {
		if ligne := strings.TrimSpace(ligne); ligne != "" {
			decoupees = append(decoupees, ligne)
		}
	}
	return decoupees
}

// dureeLisible met des minutes en français : « 45 min », « 1 h 30 », « 2 h ».
//
// Le zéro de tête de « 1 h 05 » n'est pas un ornement : « 1 h 5 » se lit comme
// une coquille, et la fiche est faite pour être lue en cuisinant.
//
// Zéro ou négatif rend "", et c'est le bloc entier qui disparaît : une durée
// absente n'est pas une durée nulle.
func dureeLisible(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes < 60:
		return fmt.Sprintf("%d min", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf("%d h", minutes/60)
	default:
		return fmt.Sprintf("%d h %02d", minutes/60, minutes%60)
	}
}

// quantiteLisible rend la quantité sans zéro inutile, virgule décimale
// comprise : 200 donne « 200 », 0.5 donne « 0,5 ». Zéro rend "" — le champ est
// facultatif, et le parser le laisse vide sur « une pincée de sel ».
func quantiteLisible(quantite float64) string {
	if quantite == 0 {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(quantite, 'f', -1, 64), ".", ",", 1)
}
