package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// donneesSuppression est ce que la page de confirmation reçoit.
//
// Les comptes y sont déjà faits, comme le reste de la fiche : le gabarit
// affiche ou n'affiche pas, et un zéro y signifie « ce libellé n'existe pas ».
type donneesSuppression struct {
	donneesPage

	// Id est la recette visée : le formulaire de confirmation et le lien
	// Annuler s'adressent tous deux à elle.
	Id string

	// Nom est le titre de la recette. Titre, sur donneesPage, est celui de
	// l'onglet du navigateur, et les deux se lisent dans le même gabarit.
	Nom string

	// Ingredients et Notes comptent ce que les deux cascades du schéma
	// emporteront. Notes porte sur celles de tous les comptes, et non sur les
	// seules siennes : supprimer sa recette efface les notes que d'autres y
	// ont laissées, et c'est précisément ce qu'on ne peut pas laisser
	// découvrir après coup.
	Ingredients int
	Notes       int

	// Image dit que la recette porte une photo, que PocketBase emporte avec
	// l'enregistrement.
	Image bool
}

// laRouteDUneSuppression traduit erreurIntrouvable en la 404 de la fiche.
//
// Le geste de laRouteDUneNote (commentaires.go), et non son appel : cette
// fonction-là est nommée d'après les notes, et la renommer pour la partager
// toucherait les quatre routes des notes — un refactor que cette tâche ne
// demande pas.
func laRouteDUneSuppression(traite func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		err := traite(e)
		if errors.Is(err, erreurIntrouvable) {
			return pageRecetteIntrouvable(e)
		}
		return err
	}
}

// laRecetteASupprimer rend la recette visée, ou erreurIntrouvable.
//
// La propriété se vérifie ici, dans le code de la route, et pas seulement par
// DeleteRule : nos routes suppriment par e.App.Delete, qui n'applique pas les
// règles de collection — celles-ci gardent l'API REST, pas notre code.
//
// erreurIntrouvable dans les deux cas, l'identifiant inconnu comme la recette
// d'un autre : l'existence d'une recette qu'on ne peut pas supprimer n'est pas
// une information à donner par un code de statut. C'est la ligne que
// laNoteDemandee tient déjà pour les notes.
func laRecetteASupprimer(e *core.RequestEvent) (*core.Record, error) {
	id := e.Request.PathValue("id")

	recette, err := e.App.FindRecordById("recipes", id)
	if err != nil {
		return nil, fmt.Errorf("recette %s : %w", id, erreurIntrouvable)
	}
	if !sienne(recette, e.Auth.Id) {
		return nil, fmt.Errorf("recette %s hors de portée : %w", recette.Id, erreurIntrouvable)
	}
	return recette, nil
}

// pageSupprimerRecette rend la confirmation, et dit ce qui part avec la
// recette.
//
// Une page rendue par le serveur plutôt qu'un hx-confirm : celui-ci s'évapore
// sans JavaScript — le formulaire partirait quand même, et le garde-fou
// disparaîtrait exactement là où il compte. C'est aussi le seul endroit où
// annoncer les notes d'autrui emportées.
func pageSupprimerRecette(e *core.RequestEvent) error {
	recette, err := laRecetteASupprimer(e)
	if err != nil {
		return err
	}

	ingredients, err := e.App.CountRecords("ingredients", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		return fmt.Errorf("décompte des ingrédients de %s : %w", recette.Id, err)
	}
	notes, err := e.App.CountRecords("comments", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		return fmt.Errorf("décompte des notes de %s : %w", recette.Id, err)
	}

	return rendre(e, "recette-supprimer.html", "recette-supprimer-corps.html", &donneesSuppression{
		donneesPage: donneesPage{Titre: "Supprimer une recette — Patachoo"},
		Id:          recette.Id,
		Nom:         recette.GetString("title"),
		Ingredients: int(ingredients),
		Notes:       int(notes),
		Image:       recette.GetString("image") != "",
	})
}

// supprimeLaRecette efface la recette, puis renvoie à la liste.
//
// Les ingrédients et les notes de tous les comptes partent avec elle par les
// deux cascades du schéma, et l'image avec l'enregistrement : rien à effacer à
// la main ici.
//
// Aucun arbitrage sur estHTMX, contrairement aux routes des notes : la fiche
// vient de disparaître, il n'y a pas de bloc à remplacer.
func supprimeLaRecette(e *core.RequestEvent) error {
	recette, err := laRecetteASupprimer(e)
	if err != nil {
		return err
	}

	if err := e.App.Delete(recette); err != nil {
		return fmt.Errorf("suppression de la recette %s : %w", recette.Id, err)
	}
	return e.Redirect(http.StatusSeeOther, "/recettes")
}
