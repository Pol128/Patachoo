package recuperation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// La cadence et le robots.txt retenu : les deux options par lesquelles un
// appelant qui enchaîne — l'import en lot — reprend la main sur les requêtes que
// ce paquet émet sans qu'il les ait demandées.
//
// Le respect du rythme lui-même se lit chez cet appelant, sur son horloge
// virtuelle. Ce qui se vérifie ici, c'est ce que ce paquet-ci lui doit : que
// chaque requête émise passe par son tour de rôle, que l'attente qu'il impose ne
// se compte dans aucune borne de temps, et que la borne d'un appel reste celle
// de l'appel entier.

// cadenceNotee note ce qu'on lui demande et ne fait attendre personne : ces
// tests mesurent ce qui est demandé, pas ce qui est respecté.
type cadenceNotee struct {
	mu      sync.Mutex
	tours   []string
	annonce time.Duration
}

func (c *cadenceNotee) AttendSonTour(_ context.Context, hote string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.tours = append(c.tours, hote)
	return nil
}

func (c *cadenceNotee) Retiens(_ string, annonce time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.annonce = annonce
}

// toursDemandes rend les hôtes dont le tour a été attendu, dans l'ordre.
func (c *cadenceNotee) toursDemandes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.tours...)
}

// delaiRapporte rend le dernier Crawl-delay que le paquet a rapporté.
func (c *cadenceNotee) delaiRapporte() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.annonce
}

// cadenceLente fait patienter chaque échange, sans rien retenir : de quoi
// vérifier que l'attente ne se prend pas sur le compte d'une borne.
type cadenceLente struct{ attente time.Duration }

func (c cadenceLente) AttendSonTour(ctx context.Context, _ string, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.attente):
		return nil
	}
}

func (c cadenceLente) Retiens(string, time.Duration) {}

// hoteDuServeur rend l'hôte d'un serveur de test, tel que la cadence le voit.
func hoteDuServeur(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("URL du serveur de test %q : %v", srv.URL, err)
	}
	return u.Hostname()
}

// TestChaqueRequeteEmiseAttendSonTour : un appel émet deux requêtes — le
// robots.txt de l'hôte, que l'appelant n'a pas demandé, puis la page. La
// promesse de l'appelant porte sur les requêtes émises : les deux passent donc
// par son tour de rôle, et non la seule qu'il a demandée.
func TestChaqueRequeteEmiseAttendSonTour(t *testing.T) {
	srv := serveurRobots(t, "User-agent: *\nDisallow: /prive\n", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "page")
	})

	rythme := &cadenceNotee{}
	if _, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv), AvecCadence(rythme)); err != nil {
		t.Fatalf("la page devait être récupérée : %v", err)
	}

	hote := hoteDuServeur(t, srv)
	if tours := rythme.toursDemandes(); !slices.Equal(tours, []string{hote, hote}) {
		t.Errorf("tours attendus %v, attendu deux fois %q — le robots.txt, puis la page", tours, hote)
	}
}

// TestLAttenteDeCadenceNEstPasComptee : l'attente qu'une cadence impose n'entre
// dans aucune borne de temps.
//
// Comptée dedans, elle rendrait injouable tout Crawl-delay supérieur au délai
// maximal — la plage que delaiAnnonceMax accepte va jusqu'à cinq minutes, sur un
// délai maximal de dix secondes par défaut. L'hôte qui demande trente secondes
// verrait toutes ses pages échouer en delai_depasse au lieu d'être espacées.
func TestLAttenteDeCadenceNEstPasComptee(t *testing.T) {
	srv := serveurRobots(t, "User-agent: *\nCrawl-delay: 5\n", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "page")
	})

	// Chaque échange attend plus que la borne, et les deux répondent en
	// quelques millisecondes.
	lente := cadenceLente{attente: 120 * time.Millisecond}
	page, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv),
		AvecDelaiMax(50*time.Millisecond), AvecCadence(lente))
	if err != nil {
		t.Fatalf("la page devait être récupérée, l'attente n'étant pas un dépassement : %v", err)
	}
	if string(page.Corps) != "page" {
		t.Errorf("corps %q, attendu %q", page.Corps, "page")
	}
}

// TestLeBudgetDeTempsEstPartageEntreLesEchanges : la borne d'un appel est celle
// de l'appel entier — robots.txt et page confondus —, et non celle d'un échange.
//
// C'est le pire cas que PATA-9 doit à son utilisateur, qui attend devant sa
// page : une borne par échange le doublerait — vingt secondes au lieu de dix par
// défaut — sans que rien ne le dise. Chaque échange rend au budget ce qu'il n'a
// pas consommé, et le suivant part avec ce qui reste.
func TestLeBudgetDeTempsEstPartageEntreLesEchanges(t *testing.T) {
	const budget = 600 * time.Millisecond
	const robotsLent = 500 * time.Millisecond

	bloque := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			time.Sleep(robotsLent)
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /prive\n")
			return
		}
		// La page ne répond jamais : ce qu'il reste du budget décide, et lui
		// seul.
		select {
		case <-bloque:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	// Enregistré après la fermeture, donc joué avant elle : un serveur qui
	// attend encore une réponse de son propre gestionnaire ne se ferme pas.
	t.Cleanup(func() { close(bloque) })

	debut := time.Now()
	_, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv), AvecDelaiMax(budget))
	ecoule := time.Since(debut)

	if cause := echec(t, err).Cause; cause != DelaiDepasse {
		t.Errorf("cause %q, attendu %q", cause, DelaiDepasse)
	}
	// Une borne par échange rendrait la main au bout de robotsLent + budget.
	const marge = 250 * time.Millisecond
	if ecoule > budget+marge {
		t.Errorf("appel rendu au bout de %v, attendu au plus %v : la borne est devenue celle d'un "+
			"échange, et le pire cas de PATA-9 a doublé", ecoule, budget+marge)
	}
}

// robotsDemandes compte les requêtes de robots.txt qu'exigent deux pages d'un
// même hôte, sous les options données.
func robotsDemandes(t *testing.T, choix ...Option) int {
	t.Helper()

	var robots atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			robots.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /prive\n")
			return
		}
		fmt.Fprint(w, "page")
	}))
	t.Cleanup(srv.Close)

	for _, chemin := range []string{"/recettes/tarte", "/recettes/gateau"} {
		if _, err := recupere(t, srv.URL+chemin, append([]Option{autorise(srv)}, choix...)...); err != nil {
			t.Fatalf("%s devait être récupéré : %v", chemin, err)
		}
	}
	return int(robots.Load())
}

// TestLeRobotsRetenuNEstLuQuUneFoisParHote : une fournée de vingt pages sur un
// même site lui coûte vingt-et-une requêtes, et non quarante. Ce qui se garde
// est la décision, pas la réponse : elle ne dépend plus du chemin demandé.
func TestLeRobotsRetenuNEstLuQuUneFoisParHote(t *testing.T) {
	if lues := robotsDemandes(t, AvecRobotsRetenus(&RobotsRetenus{})); lues != 1 {
		t.Errorf("%d robots.txt demandés pour deux pages du même hôte, attendu 1", lues)
	}
}

// TestSansOptionLeRobotsEstRedemande : les valeurs par défaut du paquet ne
// dépendent d'aucune des options nouvelles. Sans elles, le comportement est
// celui d'avant — un robots.txt par page, et aucune attente —, et c'est ce qui
// laisse le chemin de PATA-9 intact.
func TestSansOptionLeRobotsEstRedemande(t *testing.T) {
	if lues := robotsDemandes(t); lues != 2 {
		t.Errorf("%d robots.txt demandés pour deux pages du même hôte, attendu 2 : sans "+
			"AvecRobotsRetenus, rien ne se garde", lues)
	}
}

// TestLeRobotsRetenuNeVautQuePourSonHote : ce qui se garde est une décision
// d'accès, et une décision d'accès partagée entre deux hôtes est un refus qu'on
// laisse passer.
//
// Le cache est donc rangé par origine, et le test le dit dans le sens du refus
// (DOD.md §3) : l'hôte qui interdit tout reste interdit après qu'un autre a été
// autorisé, et réciproquement — dans les deux ordres, parce qu'un seul dirait
// seulement que la première décision ne s'écrase pas.
func TestLeRobotsRetenuNeVautQuePourSonHote(t *testing.T) {
	page := func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "page") }
	ferme := serveurRobots(t, "User-agent: *\nDisallow: /\n", page)
	ouvert := serveurRobots(t, "User-agent: *\nDisallow: /prive\n", page)

	cas := []struct {
		nom   string
		ordre []*httptest.Server
	}{
		{"l'hôte fermé d'abord", []*httptest.Server{ferme, ouvert}},
		{"l'hôte ouvert d'abord", []*httptest.Server{ouvert, ferme}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			retenus := &RobotsRetenus{}
			for _, srv := range c.ordre {
				_, err := recupere(t, srv.URL+"/recettes/tarte",
					autorise(ferme, ouvert), AvecRobotsRetenus(retenus))
				if srv == ouvert {
					if err != nil {
						t.Errorf("l'hôte qui n'interdit que /prive a été refusé : %v", err)
					}
					continue
				}
				if cause := echec(t, err).Cause; cause != RobotsInterdit {
					t.Errorf("cause %q pour l'hôte qui interdit tout, attendu %q", cause, RobotsInterdit)
				}
			}
		})
	}
}

// TestUnRobotsEnPanneNEntrePasDansLeCache : un robots.txt qui répond 500 fait
// renoncer — le REP demande de s'abstenir quand le serveur est en erreur — mais
// cette réponse-là est datée, pas définitive.
//
// Retenue au même titre que des règles, une panne d'une seconde condamnerait
// l'hôte entier en robots_interdit — sort définitif que rien ne rejoue — pour
// toute la durée de la fournée.
func TestUnRobotsEnPanneNEntrePasDansLeCache(t *testing.T) {
	var demandes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			if demandes.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "User-agent: *\nDisallow: /prive\n")
			return
		}
		fmt.Fprint(w, "page")
	}))
	t.Cleanup(srv.Close)

	retenus := &RobotsRetenus{}
	adresse := srv.URL + "/recettes/tarte"

	_, err := recupere(t, adresse, autorise(srv), AvecRobotsRetenus(retenus))
	if cause := echec(t, err).Cause; cause != RobotsInterdit {
		t.Fatalf("cause %q du premier appel, attendu %q", cause, RobotsInterdit)
	}

	page, err := recupere(t, adresse, autorise(srv), AvecRobotsRetenus(retenus))
	if err != nil {
		t.Fatalf("second appel : la panne du robots.txt a été retenue alors qu'elle est datée : %v", err)
	}
	if string(page.Corps) != "page" {
		t.Errorf("corps %q, attendu %q", page.Corps, "page")
	}
	if lues := demandes.Load(); lues != 2 {
		t.Errorf("%d robots.txt demandés, attendu 2 : le refus né d'un 5xx ne se garde pas", lues)
	}
}

// --- La borne de l'attente --------------------------------------------------

// cadenceOccupee tient le rôle d'un hôte déjà pris : le tour qu'elle donne est
// à `attente` d'ici, et elle renonce d'elle-même quand l'appelant a dit ne pas
// vouloir attendre autant. C'est le contrat que AvecAttenteMaxDeCadence pose,
// et que la cadence de production honore.
type cadenceOccupee struct {
	attente time.Duration

	mu     sync.Mutex
	bornes []time.Duration
}

func (c *cadenceOccupee) AttendSonTour(ctx context.Context, _ string, attenteMax time.Duration) error {
	c.mu.Lock()
	c.bornes = append(c.bornes, attenteMax)
	c.mu.Unlock()

	if attenteMax > 0 && c.attente > attenteMax {
		return ErrAttenteTropLongue
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.attente):
		return nil
	}
}

func (c *cadenceOccupee) Retiens(string, time.Duration) {}

// bornesRecues rend ce que chaque échange a annoncé comme attente maximale.
func (c *cadenceOccupee) bornesRecues() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]time.Duration(nil), c.bornes...)
}

// TestLAttenteTropLongueAbandonneLAppel : l'appelant qui a dit combien de temps
// il acceptait d'attendre son tour n'attend pas davantage.
//
// La cause est la sienne, et non delai_depasse : « ce site est occupé » et « ce
// site ne répond pas » n'appellent pas la même réaction, et les confondre
// enverrait chercher la panne au mauvais endroit. Rien ne part : le tour n'a pas
// été pris, donc aucune requête n'a été émise.
func TestLAttenteTropLongueAbandonneLAppel(t *testing.T) {
	rythme := &cadenceOccupee{attente: time.Hour}

	_, err := recupere(t, "https://exemple.test/recettes/tarte",
		AvecTransport(transportPiege{t}),
		AvecCadence(rythme),
		AvecAttenteMaxDeCadence(time.Millisecond))

	if cause := echec(t, err).Cause; cause != AttenteDeCadence {
		t.Errorf("cause %q, attendu %q", cause, AttenteDeCadence)
	}
	if bornes := rythme.bornesRecues(); !slices.Equal(bornes, []time.Duration{time.Millisecond}) {
		t.Errorf("bornes annoncées %v, attendu une seule d'une milliseconde — l'appel s'arrête au premier tour refusé", bornes)
	}
}

// TestSansBorneLAttenteDeCadenceNEstPasInterrompue : l'option est la seule
// chose qui borne le tour de rôle, et sans elle rien ne le borne.
//
// C'est ce qui laisse l'ouvrier du lot inchangé : il attend le temps qu'il
// faut, personne n'est devant son écran, et attendre est la politesse.
func TestSansBorneLAttenteDeCadenceNEstPasInterrompue(t *testing.T) {
	srv := serveurRobots(t, "User-agent: *\nDisallow: /prive\n", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "page")
	})
	rythme := &cadenceOccupee{attente: 20 * time.Millisecond}

	if _, err := recupere(t, srv.URL+"/recettes/tarte", autorise(srv), AvecCadence(rythme)); err != nil {
		t.Fatalf("la page devait être récupérée : %v", err)
	}

	for _, borne := range rythme.bornesRecues() {
		if borne != 0 {
			t.Errorf("borne annoncée %v, attendu aucune : une attente maximale s'est invitée sans être demandée", borne)
		}
	}
}
