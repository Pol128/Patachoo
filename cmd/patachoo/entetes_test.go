package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- Referrer-Policy ------------------------------------------------------

// Une page de Patachoo fait partir des requêtes vers des sites tiers — l'aperçu
// de l'image chez le site importé, le lien du pied vers les versions publiées.
// Sans en-tête, l'adresse de l'instance part avec elles, au bon vouloir du
// défaut du navigateur. Le défaut d'un navigateur n'est pas une propriété du
// produit : c'est la réponse qui doit le dire.
//
// Le montage complet de serveurDeTest, et non un RequestEvent nu : ce qui est
// en jeu est qu'un middleware lié au routeur atteigne bien la réponse.
func TestLesReponsesPortentUneReferrerPolicyNoReferrer(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	for _, cas := range reponsesDeTest(mux, cookie, recette.Id) {
		if valeur := cas.rec.Header().Get("Referrer-Policy"); valeur != "no-referrer" {
			t.Errorf("%s : Referrer-Policy vaut %q, attendu %q", cas.nom, valeur, "no-referrer")
		}
	}
}

// Le middleware s'ajoute à celui de PocketBase, il ne le remplace pas : les
// trois en-têtes que pbSecurityHeaders pose restent sur la réponse. Leurs
// valeurs appartiennent à PocketBase et peuvent changer d'une version à
// l'autre ; leur présence, elle, nous concerne.
func TestLesReponsesGardentLesEntetesDeSecuriteDePocketBase(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	for _, cas := range reponsesDeTest(mux, cookie, recette.Id) {
		for _, entete := range []string{"X-Content-Type-Options", "X-Frame-Options", "X-XSS-Protection"} {
			if cas.rec.Header().Get(entete) == "" {
				t.Errorf("%s : %s absent des en-têtes de la réponse", cas.nom, entete)
			}
		}
	}
}

// reponsesDeTest joue les deux pages témoins : celle qu'un visiteur obtient
// sans compte, et celle d'où part l'aperçu de l'image distante.
func reponsesDeTest(mux http.Handler, cookie *http.Cookie, idRecette string) []struct {
	nom string
	rec *httptest.ResponseRecorder
} {
	return []struct {
		nom string
		rec *httptest.ResponseRecorder
	}{
		{"GET /connexion", avecCookie(mux, http.MethodGet, "/connexion", nil)},
		{"GET /recettes/{id}", fiche(mux, cookie, idRecette)},
	}
}
