package main

import (
	"net/http"
	"testing"
)

// TestEntetesDeCache monte le serveur complet — le même que les tests de
// session — et lit le Cache-Control de chaque famille de route.
//
// Un seul test pour les six cas, et des sous-tests plutôt que six fonctions :
// ce qu'ils vérifient est un unique comportement, « le middleware pose
// l'en-tête là et seulement là ». Retirer l'écriture de l'en-tête doit faire
// rougir ce test, et lui seul (DOD.md §2).
//
// Le montage complet, et non un RequestEvent nu : ce qui est en jeu est qu'un
// middleware lié au routeur atteigne la réponse, et qu'il ne l'atteigne pas là
// où il ne doit pas.
func TestEntetesDeCache(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	t.Run("une page authentifiée porte private, no-store", func(t *testing.T) {
		rec := demande(mux, "/recettes/"+recette.Id, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	t.Run("la page de connexion le porte aussi", func(t *testing.T) {
		rec := demande(mux, "/connexion", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	// Un fragment ne passe pas par rendreAvecStatut mais par rendLeBlocSeul :
	// c'est l'autre écriture de réponse du produit, et elle doit être couverte
	// par le même middleware.
	t.Run("un fragment htmx le porte", func(t *testing.T) {
		rec := demande(mux, "/tags/suggestions?tags=", cookie, map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	// Une redirection n'écrit aucun corps : elle distingue un middleware lié au
	// routeur d'une écriture faite dans rendreAvecStatut, qu'elle n'atteint pas.
	t.Run("une redirection le porte", func(t *testing.T) {
		rec := demande(mux, "/recettes", nil, nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusFound)
		}
		if destination := rec.Header().Get("Location"); destination != "/connexion" {
			t.Fatalf("Location %q, attendu %q", destination, "/connexion")
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	// Les deux exclusions observables sur ce montage. no-store sur la feuille
	// de style remplacerait la mise en cache heuristique du navigateur par un
	// rechargement à chaque page ; sous /api/ vivent les vignettes, une par
	// recette de la liste.
	t.Run("un asset n'en porte aucun", func(t *testing.T) {
		rec := demande(mux, "/statique/patachoo.css", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "" {
			t.Errorf("Cache-Control %q, attendu aucun", pose)
		}
	})

	t.Run("une route de PocketBase n'en porte aucun", func(t *testing.T) {
		rec := demande(mux, "/api/health", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "" {
			t.Errorf("Cache-Control %q, attendu aucun", pose)
		}
	})

	// Le nouveau middleware s'ajoute à securityHeaders() de PocketBase, il ne
	// le remplace pas.
	t.Run("les en-têtes de sécurité de PocketBase sont toujours là", func(t *testing.T) {
		rec := demande(mux, "/recettes/"+recette.Id, cookie, nil)
		for entete, attendu := range map[string]string{
			"X-Content-Type-Options": "nosniff",
			"X-Frame-Options":        "SAMEORIGIN",
			"X-Xss-Protection":       "1; mode=block",
		} {
			if pose := rec.Header().Get(entete); pose != attendu {
				t.Errorf("%s %q, attendu %q", entete, pose, attendu)
			}
		}
	})
}

// TestPorteLeCacheControl couvre seule la décision de portée, qui est le vrai
// travail de la tâche : le préfixe plutôt que la route, pour qu'une page
// ajoutée demain soit couverte sans que personne n'y pense.
func TestPorteLeCacheControl(t *testing.T) {
	cas := []struct {
		chemin  string
		attendu bool
	}{
		{"/", true},
		{"/recettes/abc123", true},
		{"/connexion", true},
		{"/statique/patachoo.css", false},
		{"/api/files/recipes/x/y.jpg", false},
		{"/_/", false},
	}

	for _, c := range cas {
		t.Run(c.chemin, func(t *testing.T) {
			if porte := porteLeCacheControl(c.chemin); porte != c.attendu {
				t.Errorf("porteLeCacheControl(%q) = %v, attendu %v", c.chemin, porte, c.attendu)
			}
		})
	}
}
