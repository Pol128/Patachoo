package recuperation

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// lesSixCauses : le contrat que PATA-9 lit. La liste est écrite ici pour qu'en
// retirer une du code fasse rougir, plutôt que de rétrécir le contrat en
// silence — même garde-fou que casObligatoires dans jsonld/corpus_test.go.
var lesSixCauses = map[string]string{
	"injoignable":           Injoignable,
	"refus_http":            RefusHTTP,
	"refusee_par_politique": RefuseeParPolitique,
	"robots_interdit":       RobotsInterdit,
	"delai_depasse":         DelaiDepasse,
	"taille_max":            TailleMax,
}

func TestLesSixCausesSontStables(t *testing.T) {
	if len(lesSixCauses) != 6 {
		t.Fatalf("%d causes, attendu 6", len(lesSixCauses))
	}
	vues := map[string]bool{}
	for attendue, constante := range lesSixCauses {
		if constante != attendue {
			t.Errorf("la constante vaut %q, attendu %q : la chaîne est le contrat, elle ne bouge pas", constante, attendue)
		}
		if vues[constante] {
			t.Errorf("deux causes portent la même chaîne %q : PATA-9 ne pourrait pas les distinguer", constante)
		}
		vues[constante] = true
	}
}

// TestLesValeursParDefaut : les deux limites de DOD.md §3 sont écrites à un seul
// endroit, et ce sont celles du récolteur de référence.
func TestLesValeursParDefaut(t *testing.T) {
	if DelaiMaxDefaut != 10*time.Second {
		t.Errorf("délai par défaut %v, attendu 10s", DelaiMaxDefaut)
	}
	if TailleMaxDefaut != 5<<20 {
		t.Errorf("taille maximale par défaut %d, attendu %d (5 Mio)", TailleMaxDefaut, 5<<20)
	}
}

// TestSuccesApresDeuxRedirections : l'URL finale est celle du dernier saut, pas
// celle soumise. PATA-9 en fait source_url.
func TestSuccesApresDeuxRedirections(t *testing.T) {
	srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/depart":
			http.Redirect(w, r, "/etape", http.StatusMovedPermanently)
		case "/etape":
			http.Redirect(w, r, "/arrivee", http.StatusFound)
		case "/arrivee":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<html><body>gâteau</body></html>")
		default:
			http.NotFound(w, r)
		}
	})

	page, err := recupere(t, srv.URL+"/depart", autorise(srv))
	if err != nil {
		t.Fatalf("récupération : %v", err)
	}
	if attendu := "<html><body>gâteau</body></html>"; string(page.Corps) != attendu {
		t.Errorf("corps %q, attendu %q", page.Corps, attendu)
	}
	if attendue := srv.URL + "/arrivee"; page.URLFinale != attendue {
		t.Errorf("URL finale %q, attendu %q", page.URLFinale, attendue)
	}
	if attendu := "text/html; charset=utf-8"; page.TypeContenu != attendu {
		t.Errorf("type de contenu %q, attendu %q", page.TypeContenu, attendu)
	}
}

// TestLesRefusHTTPPortentLeCode : PATA-9 montre le code, elle ne le devine pas.
func TestLesRefusHTTPPortentLeCode(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			})

			_, err := recupere(t, srv.URL+"/recette", autorise(srv))
			e := echec(t, err)
			if e.Cause != RefusHTTP {
				t.Errorf("cause %q, attendu %q", e.Cause, RefusHTTP)
			}
			if e.Code != code {
				t.Errorf("code %d, attendu %d", e.Code, code)
			}
		})
	}
}

// TestLaLectureSArreteAuPlafond : une réponse plus longue que le plafond rend
// taille_max, et la lecture ne charge pas tout en mémoire. Le serveur se bloque
// après le plafond : si la lecture attendait la suite, le test s'éterniserait.
func TestLaLectureSArreteAuPlafond(t *testing.T) {
	const plafond = 64

	bloque := make(chan struct{})
	srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write(bytes.Repeat([]byte("a"), plafond+1))
		w.(http.Flusher).Flush()
		<-bloque
		w.Write(bytes.Repeat([]byte("b"), 1<<20))
	})
	defer close(bloque)

	fini := make(chan error, 1)
	go func() {
		_, err := recupere(t, srv.URL+"/recette", autorise(srv), AvecTailleMax(plafond))
		fini <- err
	}()

	select {
	case err := <-fini:
		if cause := echec(t, err).Cause; cause != TailleMax {
			t.Errorf("cause %q, attendu %q", cause, TailleMax)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("la lecture ne s'est pas arrêtée au plafond : elle attend encore la suite du corps")
	}
}

// TestLeDelaiEstTenu : un serveur plus lent que le délai configuré rend
// delai_depasse, distinct de l'injoignable.
func TestLeDelaiEstTenu(t *testing.T) {
	srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, "trop tard")
	})

	_, err := recupere(t, srv.URL+"/recette", autorise(srv), AvecDelaiMax(50*time.Millisecond))
	if cause := echec(t, err).Cause; cause != DelaiDepasse {
		t.Errorf("cause %q, attendu %q", cause, DelaiDepasse)
	}
}

// TestUnHoteInjoignableEstNomme : rien n'écoute, aucune page n'est atteinte.
func TestUnHoteInjoignableEstNomme(t *testing.T) {
	srv := serveur(t, func(w http.ResponseWriter, r *http.Request) {})
	permission := autorise(srv)
	adresse := srv.URL + "/recette"
	srv.Close()

	_, err := recupere(t, adresse, permission)
	if cause := echec(t, err).Cause; cause != Injoignable {
		t.Errorf("cause %q, attendu %q", cause, Injoignable)
	}
}

// TestLeUserAgentEstIdentifiable : sur chaque requête sortante, robots.txt
// compris, et depuis une constante unique.
func TestLeUserAgentEstIdentifiable(t *testing.T) {
	recus := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recus <- r.Header.Get("User-Agent")
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /prive\n")
			return
		}
		fmt.Fprint(w, "page")
	}))
	t.Cleanup(srv.Close)

	if _, err := recupere(t, srv.URL+"/recette", autorise(srv)); err != nil {
		t.Fatalf("récupération : %v", err)
	}
	close(recus)

	vus := 0
	for agent := range recus {
		vus++
		if !strings.Contains(agent, "Patachoo") {
			t.Errorf("User-Agent %q : il doit nous nommer", agent)
		}
		if !strings.Contains(agent, "https://github.com/Pol128/Patachoo") {
			t.Errorf("User-Agent %q : il doit porter l'URL du projet", agent)
		}
		if agent != Agent {
			t.Errorf("User-Agent %q, attendu %q : une seule constante", agent, Agent)
		}
	}
	if vus != 2 {
		t.Errorf("%d requêtes sortantes observées, attendu 2 (robots.txt puis la page)", vus)
	}
}
