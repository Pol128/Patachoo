package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/Pol128/Patachoo/internal/texte"
)

// maxTags borne une saisie au nombre de tags qu'une recette peut porter.
// Le schéma pose la même valeur sur recipes.tags : accepter au-delà ici
// créerait des tags qu'aucune recette ne pourrait ensuite référencer.
const maxTags = 20

// brancheLesHooks accroche nos hooks à l'app — un seul point de branchement,
// que main() appelle et que les tests appellent à l'identique.
//
// Sur l'app et non sur OnServe : un tag créé par une commande — une fusion,
// une migration — doit être normalisé lui aussi, et OnServe ne se déclenche
// que pour le serveur.
func brancheLesHooks(app core.App, a *analyseur) {
	app.OnRecordCreate("tags").BindFunc(normaliseLeTag)
	app.OnRecordUpdate("tags").BindFunc(normaliseLeTag)

	brancheLIngredient(app, a)
	brancheLAcces(app)
}

// normaliseLeTag met le nom en forme et recalcule le slug avant l'écriture.
//
// PocketBase déclenche OnRecordCreate avant la validation, qui précède
// l'écriture : modifier l'enregistrement ici puis appeler e.Next() suffit,
// et évite d'avoir à normaliser chez chaque appelant — ce qu'on oublierait
// un jour, précisément là où ça compte.
func normaliseLeTag(e *core.RecordEvent) error {
	saisi := e.Record.GetString("name")

	nom := nomNormalise(saisi)
	slug := texte.Slug(nom)
	if slug == "" {
		// L'index d'unicité n'accepterait un slug vide qu'une fois, et le
		// message parlerait d'une contrainte SQL plutôt que du champ fautif.
		return fmt.Errorf("name : %q ne donne aucun tag utilisable", saisi)
	}

	e.Record.Set("name", nom)
	// Toujours recalculé, jamais lu de l'appelant : un slug posté à la main
	// contournerait sinon l'unicité.
	e.Record.Set("slug", slug)

	return e.Next()
}

// nomNormalise rend la forme affichable et stockée d'un nom de tag : rogné,
// suites d'espaces internes réduites à une, en minuscules.
func nomNormalise(nom string) string {
	return strings.ToLower(strings.Join(strings.Fields(nom), " "))
}

// tagsDepuisSaisie rend les tags décrits par une saisie libre, en les créant
// au besoin. C'est le point d'entrée unique du formulaire.
//
// Le hook seul ne peut pas dédupliquer : il normalise un enregistrement, il ne
// sait pas transformer une création en réutilisation.
//
// La virgule sépare, et elle seule : « plat unique » est un tag, pas deux.
func tagsDepuisSaisie(app core.App, saisie string) ([]*core.Record, error) {
	type demande struct{ nom, slug string }

	var demandes []demande
	vus := map[string]bool{}
	for _, fragment := range strings.Split(saisie, ",") {
		nom := nomNormalise(fragment)
		slug := texte.Slug(nom)
		// Un fragment sans slug est ignoré, pas refusé : « vegetarien,, , »
		// est une virgule en trop, pas une panne de formulaire.
		if slug == "" || vus[slug] {
			continue
		}
		vus[slug] = true
		demandes = append(demandes, demande{nom: nom, slug: slug})
	}

	if len(demandes) > maxTags {
		return nil, fmt.Errorf("%d tags distincts, %d au maximum", len(demandes), maxTags)
	}

	tags := make([]*core.Record, 0, len(demandes))
	// La transaction pour la même raison que la borne vérifiée d'abord :
	// créer les premiers tags puis échouer laisserait des orphelins derrière.
	err := app.RunInTransaction(func(txApp core.App) error {
		collection, err := txApp.FindCollectionByNameOrId("tags")
		if err != nil {
			return fmt.Errorf("collection tags : %w", err)
		}

		trouves := make([]*core.Record, 0, len(demandes))
		for _, d := range demandes {
			// La valeur vient de l'utilisateur : elle passe par params,
			// jamais par concaténation dans le filtre.
			existant, err := txApp.FindFirstRecordByFilter(
				"tags", "slug = {:slug}", dbx.Params{"slug": d.slug})
			switch {
			case err == nil:
				trouves = append(trouves, existant)
				continue
			case !errors.Is(err, sql.ErrNoRows):
				return fmt.Errorf("recherche du tag %q : %w", d.slug, err)
			}

			// sql.ErrNoRows : le tag n'existe pas encore.
			nouveau := core.NewRecord(collection)
			nouveau.Set("name", d.nom)
			if err := txApp.Save(nouveau); err != nil {
				return fmt.Errorf("création du tag %q : %w", d.nom, err)
			}
			trouves = append(trouves, nouveau)
		}

		tags = trouves
		return nil
	})
	if err != nil {
		return nil, err
	}

	return tags, nil
}

// --- Le champ de saisie et ses suggestions ---------------------------------

// maxSuggestions borne ce que le champ propose. Huit tiennent sous la saisie
// sans masquer le formulaire ; au-delà, on ne choisit plus, on relit.
const maxSuggestions = 8

// suggestionDeTag est ce qu'un bouton de suggestion affiche et renvoie : le
// nom se lit, le slug se poste.
type suggestionDeTag struct {
	Nom  string
	Slug string
}

// saisieDesTags porte le champ et ses suggestions — un seul bloc, rendu par un
// seul fichier, parce que choisir une suggestion re-rend les deux.
type saisieDesTags struct {
	Valeur      string
	Suggestions []suggestionDeTag

	// Autofocus n'est posé que sur le bloc re-rendu après un choix : la saisie
	// doit reprendre là où elle en était. Sur le formulaire à son ouverture,
	// il volerait le curseur au champ du titre.
	Autofocus bool
}

// brancheLesTags pose la route des suggestions.
//
// Derrière exigeUneSession comme les autres : la liste des tags révèle le
// contenu du carnet, et un visiteur n'a pas à la lire.
func brancheLesTags(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/tags/suggestions", suggestionsDeTags).Bind(exigeUneSession())
}

// suggestionsDeTags rend le champ de saisie et les tags qu'il propose.
//
// Deux requêtes arrivent ici, et le paramètre choix les distingue : la frappe,
// qui demande des suggestions, et le clic sur l'une d'elles, qui demande le
// champ complété. Le recollage se fait donc côté serveur — c'est ce qui évite
// d'écrire du JavaScript pour recomposer la saisie dans le navigateur.
func suggestionsDeTags(e *core.RequestEvent) error {
	requete := e.Request.URL.Query()
	saisie := requete.Get("tags")

	if slug := requete.Get("choix"); slug != "" {
		choisi, err := e.App.FindFirstRecordByFilter(
			"tags", "slug = {:slug}", dbx.Params{"slug": slug})
		switch {
		case err == nil:
			saisie = saisieAvecLeChoix(saisie, choisi.GetString("name"))
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("tag choisi %q : %w", slug, err)
		}
		// sql.ErrNoRows : le slug ne désigne rien — un bouton d'une page
		// laissée ouverte pendant qu'un tag était fusionné. La saisie revient
		// telle quelle plutôt qu'amputée de son dernier fragment.
		return rendLeBlocSeul(e, "tags-saisie.html", saisieDesTags{Valeur: saisie, Autofocus: true})
	}

	proposees, err := lesSuggestions(e.App, saisie)
	if err != nil {
		return err
	}
	return rendLeBlocSeul(e, "tags-saisie.html", saisieDesTags{Valeur: saisie, Suggestions: proposees})
}

// lesSuggestions rend les tags que le dernier fragment de la saisie appelle.
//
// La recherche porte sur le slug et non sur le nom : le slug est déjà sans
// accent ni casse, donc « Vég », « vég » et « veg » proposent tous
// « végétarien », sans rien attendre de l'index de recherche. Effet de bord
// utile, texte.Slug n'ayant laissé que des lettres ASCII et des chiffres :
// les jokers « % » et « _ » ne peuvent pas survivre jusqu'au LIKE.
func lesSuggestions(app core.App, saisie string) ([]suggestionDeTag, error) {
	fragment := texte.Slug(dernierFragment(saisie))
	if fragment == "" {
		// Une saisie vide, ou finie sur une virgule : un bloc sans suggestion,
		// pas une erreur.
		return nil, nil
	}

	dejaSaisis := slugsDejaSaisis(saisie)

	// Autant de rangs en plus qu'il y a de tags à écarter : les écarter après
	// coup sur une page de huit en rendrait moins de huit. Plafonné au double,
	// sans quoi une saisie longue de fragments distincts dicterait la borne de
	// la requête — et une borne que l'utilisateur écrit n'en est pas une.
	rangs := min(maxSuggestions+len(dejaSaisis), 2*maxSuggestions)
	trouves, err := app.FindRecordsByFilter(
		"tags", "slug ~ {:fragment}", "name",
		rangs, 0, dbx.Params{"fragment": fragment})
	if err != nil {
		return nil, fmt.Errorf("suggestions de tags pour %q : %w", fragment, err)
	}

	proposees := make([]suggestionDeTag, 0, maxSuggestions)
	for _, tag := range trouves {
		if dejaSaisis[tag.GetString("slug")] {
			continue
		}
		proposees = append(proposees, suggestionDeTag{
			Nom:  tag.GetString("name"),
			Slug: tag.GetString("slug"),
		})
		if len(proposees) == maxSuggestions {
			break
		}
	}
	return proposees, nil
}

// dernierFragment rend ce qui suit la dernière virgule : c'est là, et nulle
// part ailleurs, que la saisie est en cours.
func dernierFragment(saisie string) string {
	fragments := strings.Split(saisie, ",")
	return fragments[len(fragments)-1]
}

// slugsDejaSaisis rend les slugs des fragments achevés — tous sauf le dernier.
//
// Le dernier en est exclu à dessein : c'est celui qu'on tape, et le proposer
// est justement ce qu'on attend d'une autocomplétion.
func slugsDejaSaisis(saisie string) map[string]bool {
	fragments := strings.Split(saisie, ",")

	slugs := map[string]bool{}
	for _, fragment := range fragments[:len(fragments)-1] {
		if slug := texte.Slug(fragment); slug != "" {
			slugs[slug] = true
		}
	}
	return slugs
}

// saisieAvecLeChoix remplace le dernier fragment par le nom retenu, et ouvre
// le suivant : « végétarien, pl » plus « plat unique » donne « végétarien,
// plat unique, ».
func saisieAvecLeChoix(saisie, nom string) string {
	fragments := strings.Split(saisie, ",")
	fragments[len(fragments)-1] = nom
	for i, fragment := range fragments {
		fragments[i] = strings.TrimSpace(fragment)
	}

	// La virgule finale n'est pas un ornement : sans elle, la frappe suivante
	// s'ajouterait au tag qu'on vient de choisir.
	return strings.Join(fragments, ", ") + ", "
}

// liensDesTags traduit les tags d'une recette en liens vers la liste filtrée.
func liensDesTags(tags []*core.Record) []lienDeFait {
	if len(tags) == 0 {
		return nil
	}

	liens := make([]lienDeFait, 0, len(tags))
	for _, tag := range tags {
		liens = append(liens, lienDeFait{
			URL:   lienVersLeTag(tag.GetString("slug")),
			Texte: tag.GetString("name"),
		})
	}
	return liens
}

// lienVersLeTag rend l'adresse de la liste restreinte à ce tag.
func lienVersLeTag(slug string) string {
	return "/recettes?" + url.Values{"tag": {slug}}.Encode()
}

// nomDuTag rend le nom porté par ce slug, ou le slug lui-même s'il n'en
// désigne aucun : la liste nomme ce qu'on lui a demandé de filtrer, même quand
// la demande ne correspond à rien.
func nomDuTag(app core.App, slug string) string {
	tag, err := app.FindFirstRecordByFilter("tags", "slug = {:slug}", dbx.Params{"slug": slug})
	if err != nil {
		return slug
	}
	return tag.GetString("name")
}
