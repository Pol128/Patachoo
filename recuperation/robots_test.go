package recuperation

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Le portage en Go de l'analyseur de robots.txt du récolteur de référence :
// jokers, ancre de fin, règle la plus longue, agent le plus spécifique. Aller
// chercher une page à la demande d'un humain n'est pas du crawl, mais un refus
// explicite du site se respecte.

// sousRobots demande /chemin à un serveur dont le robots.txt est celui donné, et
// vérifie au passage qu'une page refusée n'est jamais demandée.
func sousRobots(t *testing.T, robots, chemin string) error {
	t.Helper()

	var atteinte atomic.Bool
	srv := serveurRobots(t, robots, func(w http.ResponseWriter, r *http.Request) {
		atteinte.Store(true)
		fmt.Fprint(w, "page")
	})

	_, err := recupere(t, srv.URL+chemin, autorise(srv))
	if err != nil && atteinte.Load() {
		t.Errorf("%s refusé par robots.txt, et pourtant demandé au serveur", chemin)
	}
	return err
}

// autorisePar dit si robots.txt laisse passer chemin, et échoue sur toute autre
// cause : un serveur cassé ne doit pas se lire comme un refus de robots.
func autorisePar(t *testing.T, robots, chemin string) bool {
	t.Helper()

	err := sousRobots(t, robots, chemin)
	if err == nil {
		return true
	}
	if cause := echec(t, err).Cause; cause != RobotsInterdit {
		t.Fatalf("cause %q, attendu %q", cause, RobotsInterdit)
	}
	return false
}

func TestLesReglesDeRobots(t *testing.T) {
	cas := []struct {
		nom      string
		robots   string
		chemin   string
		autorise bool
	}{
		{"tout interdit", "User-agent: *\nDisallow: /\n", "/recettes/tarte", false},
		{"joker refuse", "User-agent: *\nDisallow: /recettes/recette-0*\n", "/recettes/recette-0123", false},
		{"joker laisse passer", "User-agent: *\nDisallow: /recettes/recette-0*\n", "/recettes/recette-9123", true},
		{"ancre de fin refuse", "User-agent: *\nDisallow: /a$\n", "/a", false},
		{"ancre de fin laisse passer", "User-agent: *\nDisallow: /a$\n", "/ab", true},
		{"règle la plus longue", "User-agent: *\nDisallow: /dossier\nAllow: /dossier/ok\n", "/dossier/ok", true},
		{"règle la plus longue, l'autre chemin", "User-agent: *\nDisallow: /dossier\nAllow: /dossier/ok\n", "/dossier/autre", false},
		{"disallow vide n'interdit rien", "User-agent: *\nDisallow:\n", "/recettes/tarte", true},
		{"commentaire ignoré", "# rien à voir\nUser-agent: *\nDisallow: / # tout\n", "/recettes/tarte", false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if autorisePar(t, c.robots, c.chemin) != c.autorise {
				t.Errorf("chemin %q : autorisé=%t, attendu %t", c.chemin, !c.autorise, c.autorise)
			}
		})
	}
}

// TestNotreJetonDAgentLEmporteSurLEtoile : le groupe le plus spécifique gagne,
// et il gagne entièrement — les règles du groupe * ne s'y ajoutent pas.
func TestNotreJetonDAgentLEmporteSurLEtoile(t *testing.T) {
	robots := "User-agent: *\nDisallow: /\n\nUser-agent: patachoo\nDisallow: /prive\n"

	if !autorisePar(t, robots, "/recettes/tarte") {
		t.Error("/recettes/tarte refusé : la règle visant notre agent doit l'emporter sur le groupe *")
	}
	if autorisePar(t, robots, "/prive/notes") {
		t.Error("/prive/notes autorisé : la règle visant notre agent doit s'appliquer")
	}
}

// TestDeuxUserAgentConsecutifsPartagentLeBloc : les règles suivent le dernier
// User-agent d'une suite, mais valent pour tous ceux de la suite. Si chaque
// ligne ouvrait son propre groupe, le nôtre serait vide et laisserait passer.
func TestDeuxUserAgentConsecutifsPartagentLeBloc(t *testing.T) {
	robots := "User-agent: patachoo\nUser-agent: bidule\nDisallow: /recettes\n"

	if autorisePar(t, robots, "/recettes/tarte") {
		t.Error("/recettes/tarte autorisé : deux User-agent consécutifs partagent le même bloc de règles")
	}
}

// TestRobotsIndisponible : une absence n'est pas un refus, mais une panne en est
// un — le REP demande de s'abstenir quand le serveur est en panne.
func TestRobotsIndisponible(t *testing.T) {
	cas := map[string]struct {
		code   int
		attend string
	}{
		"404 laisse passer": {http.StatusNotFound, ""},
		"403 laisse passer": {http.StatusForbidden, ""},
		"500 fait renoncer": {http.StatusInternalServerError, RobotsInterdit},
	}

	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/robots.txt" {
					w.WriteHeader(c.code)
					return
				}
				fmt.Fprint(w, "page")
			}))
			t.Cleanup(srv.Close)

			page, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv))
			if c.attend == "" {
				if err != nil {
					t.Fatalf("la page devait être récupérée : %v", err)
				}
				if string(page.Corps) != "page" {
					t.Errorf("corps %q, attendu %q", page.Corps, "page")
				}
				return
			}
			if cause := echec(t, err).Cause; cause != c.attend {
				t.Errorf("cause %q, attendu %q", cause, c.attend)
			}
		})
	}
}

// TestRobotsTropLent : aucune page n'a été atteinte, et c'est robots.txt qui
// n'a pas répondu — l'appel rend injoignable, pas delai_depasse.
func TestRobotsTropLent(t *testing.T) {
	var atteinte atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			time.Sleep(300 * time.Millisecond)
			return
		}
		atteinte.Store(true)
		fmt.Fprint(w, "page")
	}))
	t.Cleanup(srv.Close)

	_, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv), AvecDelaiMax(50*time.Millisecond))
	if cause := echec(t, err).Cause; cause != Injoignable {
		t.Errorf("cause %q, attendu %q", cause, Injoignable)
	}
	if atteinte.Load() {
		t.Error("la page a été demandée alors que robots.txt n'avait pas répondu")
	}
}
