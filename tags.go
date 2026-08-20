package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/Pol128/Patachoo/internal/texte"
)

// maxTags borne une saisie au nombre de tags qu'une recette peut porter.
// Le schéma pose la même valeur sur recipes.tags : accepter au-delà ici
// créerait des tags qu'aucune recette ne pourrait ensuite référencer.
const maxTags = 20

// brancheLesHooks accroche nos règles de modèle à l'app.
//
// Sur l'app et non sur OnServe : un tag créé par une commande — une fusion,
// une migration — doit être normalisé lui aussi, et OnServe ne se déclenche
// que pour le serveur.
func brancheLesHooks(app core.App, a *analyseur) {
	app.OnRecordCreate("tags").BindFunc(normaliseLeTag)
	app.OnRecordUpdate("tags").BindFunc(normaliseLeTag)

	brancheLIngredient(app, a)
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
