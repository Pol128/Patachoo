package recuperation

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// robotsDeTaille fabrique un robots.txt qui commence par entete, se poursuit en
// une seule ligne de commentaire jusqu'à l'octet jusqua, et finit par queue.
//
// Le bourrage tient sur une ligne unique pour que l'octet où le plafond tombe
// se calcule à la main, sans compter des sauts de ligne.
func robotsDeTaille(t *testing.T, entete string, jusqua int, queue string) string {
	t.Helper()

	bourrage := jusqua - len(entete)
	if bourrage < 2 {
		t.Fatalf("bourrage de %d octets : l'en-tête %q ne tient pas sous %d", bourrage, entete, jusqua)
	}
	// « # », les x, puis le saut de ligne : exactement bourrage octets.
	return entete + "#" + strings.Repeat("x", bourrage-2) + "\n" + queue
}

// TestLeRobotsTxtALeSienDePlafond : le robots.txt n'emprunte plus le plafond de
// la page (5 Mio). Ce qui suit son propre plafond n'est pas lu, donc la
// directive posée en queue de fichier ne s'applique pas — et le dépassement
// n'est pas une erreur : le REP demande d'appliquer ce qu'on a lu.
func TestLeRobotsTxtALeSienDePlafond(t *testing.T) {
	robots := robotsDeTaille(t, "User-agent: *\n", tailleMaxRobots+1, "Disallow: /recettes\n")

	if !autorisePar(t, robots, "/recettes/tarte") {
		t.Error("la queue du robots.txt, au-delà du plafond, a été appliquée : le fichier emprunte encore le plafond de la page")
	}
}

// TestLaDirectiveCoupeeParLePlafondEstEcartee : le plafond tranche où il tombe,
// éventuellement au milieu d'une directive. « Disallow: /recettes » tronqué en
// « Disallow: /rec » interdirait bien plus que le site ne l'a écrit : ce qui
// suit le dernier saut de ligne effectivement lu est donc écarté.
func TestLaDirectiveCoupeeParLePlafondEstEcartee(t *testing.T) {
	robots := robotsDeTaille(t, "User-agent: *\n", tailleMaxRobots-len("Disallow: /rec"), "Disallow: /recettes\n")

	if !autorisePar(t, robots, "/recettes/tarte") {
		t.Error("la dernière ligne, coupée par le plafond, a été analysée : un motif tronqué interdit plus que le site ne l'a écrit")
	}
}

// TestLesMotifsSontCompilesALAnalyse : l'expression d'un motif à joker ou à
// ancre est fabriquée une fois, à l'analyse. Recompilée à chaque chemin jugé,
// elle l'était pour chaque règle du groupe et pour chaque page de l'hôte — un
// robots.txt à cinq cent mille règles coûtait alors une seconde de calcul par
// page. Le motif sans joker n'en porte pas : il se compare par préfixe.
func TestLesMotifsSontCompilesALAnalyse(t *testing.T) {
	lu := analyseRobots("User-agent: *\nDisallow: /recettes/recette-0*\nDisallow: /dossier\nDisallow: /a$\n")

	if len(lu.groupes) != 1 {
		t.Fatalf("%d groupes, attendu 1", len(lu.groupes))
	}
	cas := []struct {
		motif    string
		compilee bool
	}{
		{"/recettes/recette-0*", true},
		{"/dossier", false},
		{"/a$", true},
	}
	regles := lu.groupes[0].regles
	if len(regles) != len(cas) {
		t.Fatalf("%d règles, attendu %d", len(regles), len(cas))
	}
	for i, c := range cas {
		if regles[i].motif != c.motif {
			t.Fatalf("règle %d : motif %q, attendu %q", i, regles[i].motif, c.motif)
		}
		if porte := regles[i].expression != nil; porte != c.compilee {
			t.Errorf("motif %q : expression compilée=%t, attendu %t", c.motif, porte, c.compilee)
		}
	}
}

// reglesEnSerie rend n directives d'un même champ, toutes distinctes, sous la
// forme que prend une règle à joker — celle qui coûte le plus cher à retenir.
func reglesEnSerie(champ string, n int) string {
	var lignes strings.Builder
	for i := range n {
		fmt.Fprintf(&lignes, "%s: /a%d*b\n", champ, i)
	}
	return lignes.String()
}

// TestSeulLeGroupeQuiNousViseEstRetenu : ce qui entre dans RobotsRetenus y reste
// pour toute la durée de la fournée. Les groupes des autres agents ne seront
// jamais relus — autorise et delaiPour n'en consultent qu'un — et les garder
// faisait tenir à un lot de 500 hôtes des gibioctets de règles mortes.
func TestSeulLeGroupeQuiNousViseEstRetenu(t *testing.T) {
	lu := analyseRobots("User-agent: googlebot\nDisallow: /g\n\nUser-agent: patachoo\nCrawl-delay: 2\nDisallow: /prive\n")
	if len(lu.groupes) != 2 {
		t.Fatalf("%d groupes analysés, attendu 2", len(lu.groupes))
	}

	retenu := lu.pourNous(Agent)
	if len(retenu.groupes) != 1 {
		t.Fatalf("%d groupes retenus, attendu 1 — les groupes qui ne nous visent pas sont gardés pour rien", len(retenu.groupes))
	}
	if regles := retenu.groupes[0].regles; len(regles) != 1 || regles[0].motif != "/prive" {
		t.Fatalf("règles retenues %v, attendu la seule /prive", regles)
	}
	// Le groupe gardé garde ses jetons : le robots réduit doit se relire
	// exactement comme l'original.
	if retenu.autorise("/prive/notes", Agent) {
		t.Error("/prive/notes autorisé après réduction : le groupe retenu ne se désigne plus lui-même")
	}
	if delai := retenu.delaiPour(Agent); delai != 2*time.Second {
		t.Errorf("Crawl-delay %s après réduction, attendu 2s", delai)
	}
}

// TestLesReglesDUnAutreAgentNeFontPasTaireLesNotres : la borne se compte sur le
// seul groupe retenu. Comptée tous groupes confondus, elle se laisserait
// épuiser par les règles d'un autre agent — un robots.txt qui ouvre par cinq
// cents lignes pour Googlebot avant de nous nommer ferait taire les nôtres, et
// nous irions demander un chemin que le site nous a explicitement interdit.
func TestLesReglesDUnAutreAgentNeFontPasTaireLesNotres(t *testing.T) {
	robots := "User-agent: googlebot\n" + reglesEnSerie("Disallow", reglesMaxRetenues+100) +
		"\nUser-agent: patachoo\nDisallow: /prive\n"

	if autorisePar(t, robots, "/prive/notes") {
		t.Error("/prive/notes autorisé : les règles d'un autre agent ont épuisé la borne et fait taire la nôtre")
	}
}

// TestLeNombreDeReglesRetenuesEstBorne : le plafond de taille borne le fichier
// lu, jamais ce qu'il en reste. 512 Kio de directives, ce sont encore quelque
// 26 000 règles, et une règle à joker retenue avec son expression compilée pèse
// ~1 125 octets — une fournée de 500 hôtes en tenait ~14 Gio.
func TestLeNombreDeReglesRetenuesEstBorne(t *testing.T) {
	lu := analyseRobots("User-agent: *\n" + reglesEnSerie("Disallow", reglesMaxRetenues+10))

	retenu := lu.pourNous(Agent)
	if len(retenu.groupes) != 1 {
		t.Fatalf("%d groupes retenus, attendu 1", len(retenu.groupes))
	}
	if n := len(retenu.groupes[0].regles); n != reglesMaxRetenues {
		t.Errorf("%d règles retenues, attendu %d — ce qu'un hôte retient n'est borné que par la taille du fichier", n, reglesMaxRetenues)
	}
}
