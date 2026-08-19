// Package jsonld lit les recettes que les sites publient en JSON-LD
// schema.org, et rend ce qu'on en retient.
//
// L'extraction elle-même est PATA-7. Ce qui est ici, c'est ce sur quoi elle
// sera jugée : le type de sortie, et un corpus de test écrit de toutes pièces.
package jsonld

// Recette est ce qu'on retient d'une page. Tous les champs sont facultatifs
// sauf le titre : un site peut publier une recette sans durée, sans portions,
// sans image, et elle reste une recette.
type Recette struct {
	Titre       string `json:"titre"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	Portions    string `json:"portions,omitempty"`
	// Durées en minutes, telles qu'on les affichera. Le JSON-LD les publie en
	// ISO 8601 (PT1H30M), la conversion fait partie de l'extraction.
	PreparationMin int      `json:"preparation_min,omitempty"`
	CuissonMin     int      `json:"cuisson_min,omitempty"`
	Ingredients    []string `json:"ingredients,omitempty"`
	Etapes         []string `json:"etapes,omitempty"`
}

// Échecs possibles de l'extraction. Ils sont nommés parce que l'utilisateur ne
// doit pas lire « une erreur est survenue » : les causes n'appellent pas la
// même réaction de sa part.
const (
	// SansRecette : la page publie du JSON-LD, mais aucune entité Recipe.
	// Mesuré à 8,7 % du corpus.
	SansRecette = "sans_recette"
	// AucunBalisage : pas le moindre bloc JSON-LD. 12,1 % du corpus.
	AucunBalisage = "aucun_balisage"
	// JSONInvalide : un bloc ld+json présent, mais illisible.
	JSONInvalide = "json_invalide"
	// TitreAbsent : une entité Recipe sans nom. Sans titre, il n'y a pas de
	// fiche à montrer.
	TitreAbsent = "titre_absent"
)
