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
		{"à égalité, Allow gagne", "User-agent: *\nDisallow: /dossier\nAllow: /dossier\n", "/dossier/ok", true},
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

// --- Le Crawl-delay ---------------------------------------------------------

// Le Crawl-delay est lu ici mais respecté ailleurs : ce paquet va chercher une
// page, il n'en enchaîne pas. C'est l'import en lot (PATA-42) qui espace ses
// requêtes de ce que l'hôte demande, et il ne peut le faire que si on le lui
// dit — par la cadence qu'il fournit, avant que la page ne soit demandée.

// delaiAnnoncePar rend le Crawl-delay que ce robots.txt nous adresse, tel que
// l'appelant l'apprend — par sa cadence, seul chemin par lequel ce paquet le
// rapporte.
func delaiAnnoncePar(t *testing.T, robots string) time.Duration {
	t.Helper()

	srv := serveurRobots(t, robots, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "page")
	})

	rythme := &cadenceNotee{}
	if _, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv), AvecCadence(rythme)); err != nil {
		t.Fatalf("la page devait être récupérée : %v", err)
	}
	return rythme.delaiRapporte()
}

func TestLeCrawlDelayAnnonce(t *testing.T) {
	cas := []struct {
		nom     string
		robots  string
		attendu time.Duration
	}{
		{"aucun robots.txt", "", 0},
		{"aucune directive", "User-agent: *\nDisallow: /prive\n", 0},
		{"annoncé au groupe *", "User-agent: *\nCrawl-delay: 5\n", 5 * time.Second},
		{"fractionnaire", "User-agent: *\nCrawl-delay: 0.5\n", 500 * time.Millisecond},
		{"valeur illisible", "User-agent: *\nCrawl-delay: bientôt\n", 0},
		{"valeur négative", "User-agent: *\nCrawl-delay: -3\n", 0},
		{"directive avant tout groupe", "Crawl-delay: 7\nUser-agent: *\nDisallow:\n", 0},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if lu := delaiAnnoncePar(t, c.robots); lu != c.attendu {
				t.Errorf("Crawl-delay lu %v, attendu %v", lu, c.attendu)
			}
		})
	}
}

// TestLeCrawlDelayEstCeluiDuGroupeQuiNousVise : celui d'un autre agent ne nous
// concerne pas. Nous ralentir de trente secondes parce que Googlebot est prié
// de le faire, c'est s'infliger un refus qui ne nous vise pas.
func TestLeCrawlDelayEstCeluiDuGroupeQuiNousVise(t *testing.T) {
	cas := []struct {
		nom     string
		robots  string
		attendu time.Duration
	}{
		{
			"celui d'un autre agent est ignoré",
			"User-agent: Googlebot\nCrawl-delay: 30\n\nUser-agent: *\nDisallow:\n",
			0,
		},
		{
			"le nôtre l'emporte sur celui de l'étoile",
			"User-agent: *\nCrawl-delay: 30\n\nUser-agent: patachoo\nCrawl-delay: 2\n",
			2 * time.Second,
		},
		{
			"celui de l'étoile s'applique faute de mieux",
			"User-agent: Googlebot\nCrawl-delay: 30\n\nUser-agent: *\nCrawl-delay: 3\n",
			3 * time.Second,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if lu := delaiAnnoncePar(t, c.robots); lu != c.attendu {
				t.Errorf("Crawl-delay lu %v, attendu %v", lu, c.attendu)
			}
		})
	}
}

// TestLeCrawlDelayEstBorne : la valeur vient d'un tiers, et une valeur codée
// sans test finit augmentée. Un robots.txt qui annonce une journée d'attente
// tiendrait un lot en otage sans jamais rien refuser explicitement.
//
// Les valeurs énormes ne sont pas une curiosité : la borne se compare après
// conversion en durée, et une durée déborde au-delà d'environ 9,2×10⁹
// secondes. Un débordement rend une valeur négative, que la borne laisse
// passer et que le cadencement écarte ensuite — l'hôte retombe alors sur notre
// seconde par défaut, c'est-à-dire l'inverse de ce que la borne promet.
func TestLeCrawlDelayEstBorne(t *testing.T) {
	cas := []struct {
		valeur  string
		attendu time.Duration
	}{
		{"86400", delaiAnnonceMax},
		{"9999999999", delaiAnnonceMax},
		{"1e300", delaiAnnonceMax},
		{"Infinity", delaiAnnonceMax},
		// Ni un délai, ni zéro : NaN n'est ni plus grand ni plus petit que
		// quoi que ce soit, et aucune comparaison ne l'écarte. Ce qu'on ne
		// sait pas lire ne ralentit rien.
		{"NaN", 0},
	}

	for _, c := range cas {
		t.Run("Crawl-delay: "+c.valeur, func(t *testing.T) {
			if lu := delaiAnnoncePar(t, "User-agent: *\nCrawl-delay: "+c.valeur+"\n"); lu != c.attendu {
				t.Errorf("Crawl-delay lu %v, attendu %v", lu, c.attendu)
			}
		})
	}
}
