package recuperation

import (
	"fmt"
	"net/http"
	"testing"
)

// Le point le plus dangereux du produit (DOD.md §3) : c'est le serveur qui va
// chercher une URL fournie par l'utilisateur. Ces tests disent ce qu'il refuse.

// TestLesPlagesRefusees couvre la liste que DOD.md §3 et la tâche énumèrent.
// `localhost` passe par la résolution injectée : le refus doit tomber sur
// l'adresse résolue, pas sur la chaîne.
func TestLesPlagesRefusees(t *testing.T) {
	cas := map[string]string{
		"localhost":            "http://localhost/recette",
		"127.0.0.1":            "http://127.0.0.1/recette",
		"127.0.0.0/8 ailleurs": "http://127.9.9.9/recette",
		"métadonnées cloud":    "http://169.254.169.254/latest/meta-data/",
		"10/8":                 "http://10.0.0.1/recette",
		"172.16/12":            "http://172.16.0.1/recette",
		"192.168/16":           "http://192.168.0.1/recette",
		"non spécifiée":        "http://0.0.0.0/recette",
		"::1":                  "http://[::1]/recette",
		"fc00::/7":             "http://[fc00::1]/recette",
		"mappée v4":            "http://[::ffff:127.0.0.1]/recette",
	}

	resolution := avecResolution(map[string]string{"localhost": "127.0.0.1"})
	for nom, adresse := range cas {
		t.Run(nom, func(t *testing.T) {
			_, err := recupere(t, adresse, resolution)
			if cause := echec(t, err).Cause; cause != RefuseeParPolitique {
				t.Errorf("cause %q, attendu %q", cause, RefuseeParPolitique)
			}
		})
	}
}

// TestLeRefusPorteSurLAdresseResoluePasSurLeNom : sans lui, un filtre sur la
// chaîne du nom d'hôte passerait tous les tests précédents.
func TestLeRefusPorteSurLAdresseResoluePasSurLeNom(t *testing.T) {
	resolution := avecResolution(map[string]string{"recettes.example": "10.0.0.1"})

	_, err := recupere(t, "http://recettes.example/gateau", resolution)
	if cause := echec(t, err).Cause; cause != RefuseeParPolitique {
		t.Errorf("cause %q, attendu %q : un nom d'apparence publique qui résout vers une plage privée doit être refusé", cause, RefuseeParPolitique)
	}
}

// TestLesSchemasRefusesNeFontPartirAucuneRequete : la validation précède le
// réseau. Le transport piégé le prouve.
func TestLesSchemasRefusesNeFontPartirAucuneRequete(t *testing.T) {
	cas := map[string]string{
		"ftp":      "ftp://exemple.test/recette",
		"file":     "file:///etc/passwd",
		"relative": "/recette",
		"vide":     "",
	}

	for nom, adresse := range cas {
		t.Run(nom, func(t *testing.T) {
			_, err := recupere(t, adresse, avecTransport(transportPiege{t}))
			if cause := echec(t, err).Cause; cause != RefuseeParPolitique {
				t.Errorf("cause %q, attendu %q", cause, RefuseeParPolitique)
			}
		})
	}
}

// TestUneRedirectionEstReverifiee : le premier saut est public, le second ne
// l'est pas. La politique vaut à chaque saut, pas seulement à l'entrée.
func TestUneRedirectionEstReverifiee(t *testing.T) {
	cas := map[string]string{
		"plage privée":  "http://10.0.0.1/vole",
		"boucle locale": "http://127.0.0.1:9/vole",
		"ftp":           "ftp://exemple.test/vole",
		"file":          "file:///etc/passwd",
	}

	for nom, vers := range cas {
		t.Run(nom, func(t *testing.T) {
			srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", vers)
				w.WriteHeader(http.StatusFound)
			})

			_, err := recupere(t, srv.URL+"/recette", autorise(srv))
			if cause := echec(t, err).Cause; cause != RefuseeParPolitique {
				t.Errorf("cause %q, attendu %q", cause, RefuseeParPolitique)
			}
		})
	}
}

// TestLesRedirectionsSontComptees : cinq sauts aboutissent, six s'arrêtent.
// Aucune page n'a été atteinte au sixième : la cause est injoignable.
func TestLesRedirectionsSontComptees(t *testing.T) {
	srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fin" {
			fmt.Fprint(w, "arrivé")
			return
		}
		var reste int
		if _, err := fmt.Sscanf(r.URL.Path, "/saut/%d", &reste); err != nil {
			http.NotFound(w, r)
			return
		}
		suite := "/fin"
		if reste > 1 {
			suite = fmt.Sprintf("/saut/%d", reste-1)
		}
		http.Redirect(w, r, suite, http.StatusFound)
	})

	page, err := recupere(t, srv.URL+"/saut/5", autorise(srv))
	if err != nil {
		t.Fatalf("cinq redirections doivent aboutir : %v", err)
	}
	if string(page.Corps) != "arrivé" {
		t.Errorf("corps %q, attendu %q", page.Corps, "arrivé")
	}

	_, err = recupere(t, srv.URL+"/saut/6", autorise(srv))
	if cause := echec(t, err).Cause; cause != Injoignable {
		t.Errorf("cause %q, attendu %q", cause, Injoignable)
	}
}
