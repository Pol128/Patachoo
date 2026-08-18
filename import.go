package main

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

// importDepuisURL recevra l'URL collée par l'utilisateur et rendra une fiche
// pré-remplie. La récupération de la page distante est PATA-8, l'extraction
// JSON-LD PATA-7, et le parcours utilisateur complet PATA-9.
//
// En attendant, la route existe et le dit franchement : un 501 explicite vaut
// mieux qu'un 404 qui laisse croire à une faute de frappe dans l'URL.
func importDepuisURL(e *core.RequestEvent) error {
	return e.JSON(http.StatusNotImplemented, aFaire("import par URL", "PATA-9"))
}

// aFaire décrit une route déclarée mais pas encore écrite.
func aFaire(quoi, tache string) map[string]string {
	return map[string]string{
		"message": quoi + " : pas encore implémenté",
		"tache":   tache,
	}
}
