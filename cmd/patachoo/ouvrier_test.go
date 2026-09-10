package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/Pol128/Patachoo/jsonld"
	"github.com/Pol128/Patachoo/recuperation"
)

// L'ouvrier de l'import en lot : une file par hôte, un cadencement, une
// reprise.
//
// Aucun test ne sort sur le réseau et aucun ne patiente. Le temps est une
// horloge virtuelle qui ne saute que lorsque toutes les files attendent, et le
// réseau un site factice qui note l'instant de chaque appel sur cette
// horloge-là. C'est ce qui permet de vérifier un écart d'une seconde en
// quelques microsecondes.

// --- L'horloge virtuelle ----------------------------------------------------

// departDesTests est l'instant zéro : une date fixe plutôt que time.Now(),
// pour qu'un écart lu sur l'horloge se raconte en secondes depuis le départ.
var departDesTests = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// horlogeVirtuelle est le temps des tests. Il ne passe pas tout seul : il
// saute à la prochaine échéance quand toutes les files annoncées attendent,
// c'est-à-dire quand plus rien ne peut avancer sans lui.
//
// Cette condition-là est ce qui rend les tests reproductibles : une horloge
// qui avancerait à l'aveugle pourrait faire courir une file pendant qu'une
// autre n'a pas encore émis sa première requête, et le cadencement se lirait
// alors comme une mise en file d'attente.
type horlogeVirtuelle struct {
	mu         sync.Mutex
	reveil     *sync.Cond
	maintenant time.Time
	files      int
	// sommet est le plus grand nombre de files menées de front depuis le
	// début. Une file est séquentielle — une requête à la fois, puis
	// l'attente — donc c'est aussi le plus grand nombre de requêtes
	// sortantes qui ont été en vol ensemble.
	sommet    int
	echeances []*time.Time
}

func nouvelleHorlogeVirtuelle() *horlogeVirtuelle {
	h := &horlogeVirtuelle{maintenant: departDesTests}
	h.reveil = sync.NewCond(&h.mu)
	return h
}

func (h *horlogeVirtuelle) Maintenant() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.maintenant
}

func (h *horlogeVirtuelle) Files(delta int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.files += delta
	if h.files > h.sommet {
		h.sommet = h.files
	}
	// Une file qui se ferme peut être la dernière que les autres attendaient.
	h.reveil.Broadcast()
}

func (h *horlogeVirtuelle) Attends(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	// Un sync.Cond ne se réveille pas sur une annulation de contexte : c'est à
	// nous de le pousser.
	arrete := context.AfterFunc(ctx, func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		h.reveil.Broadcast()
	})
	defer arrete()

	h.mu.Lock()
	defer h.mu.Unlock()

	echeance := h.maintenant.Add(d)
	h.echeances = append(h.echeances, &echeance)
	defer h.retire(&echeance)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !h.maintenant.Before(echeance) {
			return nil
		}
		if len(h.echeances) >= h.files && h.avance() {
			continue
		}
		h.reveil.Wait()
	}
}

// avance porte l'horloge à la première échéance et dit si le temps a bougé.
func (h *horlogeVirtuelle) avance() bool {
	premiere := *h.echeances[0]
	for _, e := range h.echeances[1:] {
		if e.Before(premiere) {
			premiere = *e
		}
	}
	if !premiere.After(h.maintenant) {
		// Une échéance déjà échue : son dormeur n'a pas encore été réveillé.
		return false
	}

	h.maintenant = premiere
	h.reveil.Broadcast()
	return true
}

func (h *horlogeVirtuelle) retire(echeance *time.Time) {
	for i, e := range h.echeances {
		if e == echeance {
			h.echeances = append(h.echeances[:i], h.echeances[i+1:]...)
			h.reveil.Broadcast()
			return
		}
	}
}

// depuisLeDepart rend le temps écoulé sur l'horloge, en secondes de cadence.
func depuisLeDepart(instant time.Time) time.Duration {
	return instant.Sub(departDesTests)
}

// sommetDesFiles rend le plus grand nombre de files que l'ouvrier a menées de
// front.
func (h *horlogeVirtuelle) sommetDesFiles() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.sommet
}

// --- Le site factice --------------------------------------------------------

// reponseDuSite est ce qu'une URL rapporte : la page que PATA-8 aurait
// ramenée, ou l'échec nommé qu'elle ou PATA-7 rendrait.
type reponseDuSite struct {
	page recuperation.Page
	err  error
	// apres est joué une fois la réponse rendue. C'est par là qu'un test
	// coupe le contexte au milieu d'un lot, là où l'arrêt du serveur le
	// couperait.
	apres func()
}

// appelSortant garde ce qu'une requête a demandé, et quand — sur l'horloge
// injectée, la seule qui avance dans ces tests.
type appelSortant struct {
	url     string
	hote    string
	instant time.Time
}

// siteFactice tient lieu de réseau : il rend ce que la table décrit et note
// chaque appel.
type siteFactice struct {
	horloge  *horlogeVirtuelle
	mu       sync.Mutex
	reponses map[string]reponseDuSite
	vus      []appelSortant
}

// avecSite branche le site factice sur la couture de l'import unitaire, la
// même que l'ouvrier emprunte : les refus SSRF, les plafonds et les causes
// nommées sont l'affaire de PATA-8, testée chez elle.
func avecSite(t *testing.T, h *horlogeVirtuelle, reponses map[string]reponseDuSite) *siteFactice {
	t.Helper()

	s := &siteFactice{horloge: h, reponses: reponses}
	avecRecuperateur(t, s.recupere)
	return s
}

func (s *siteFactice) recupere(_ context.Context, adresse string, _ ...recuperation.Option) (recuperation.Page, error) {
	s.mu.Lock()
	s.vus = append(s.vus, appelSortant{url: adresse, hote: hoteDe(adresse), instant: s.horloge.Maintenant()})
	rendue, connue := s.reponses[adresse]
	s.mu.Unlock()

	if !connue {
		return recuperation.Page{}, &recuperation.Erreur{Cause: recuperation.Injoignable, URL: adresse}
	}
	if rendue.apres != nil {
		rendue.apres()
	}
	return rendue.page, rendue.err
}

// appels rend les requêtes émises, dans l'ordre.
func (s *siteFactice) appels() []appelSortant {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]appelSortant(nil), s.vus...)
}

// appelsVers rend les requêtes émises vers un hôte, dans l'ordre.
func (s *siteFactice) appelsVers(hote string) []appelSortant {
	return versLHote(s.appels(), hote)
}

// versLHote filtre des appels sur leur hôte, en gardant leur ordre.
func versLHote(appels []appelSortant, hote string) []appelSortant {
	var vers []appelSortant
	for _, a := range appels {
		if a.hote == hote {
			vers = append(vers, a)
		}
	}
	return vers
}

// ecartsEntre rend les écarts entre requêtes consécutives, lus sur l'horloge
// virtuelle.
func ecartsEntre(appels []appelSortant) []time.Duration {
	var ecarts []time.Duration
	for i := 1; i < len(appels); i++ {
		ecarts = append(ecarts, appels[i].instant.Sub(appels[i-1].instant))
	}
	return ecarts
}

// --- Le réseau, là où les requêtes partent vraiment -------------------------

// reseauFactice tient lieu de transport HTTP. Il répond à la place du réseau et
// note chaque requête émise — le robots.txt compris.
//
// C'est le seul montage qui voie ce que la couture recuperePage cache : un appel
// de recuperation, ce sont deux requêtes sortantes, et la promesse de
// cadencement porte sur celles-là, pas sur les appels de la couture.
type reseauFactice struct {
	horloge *horlogeVirtuelle
	mu      sync.Mutex
	corps   map[string]string
	vus     []appelSortant
}

func (r *reseauFactice) RoundTrip(requete *http.Request) (*http.Response, error) {
	adresse := requete.URL.String()

	r.mu.Lock()
	r.vus = append(r.vus, appelSortant{
		url:     adresse,
		hote:    strings.ToLower(requete.URL.Hostname()),
		instant: r.horloge.Maintenant(),
	})
	corps, connu := r.corps[adresse]
	r.mu.Unlock()

	statut := http.StatusOK
	if !connu {
		statut, corps = http.StatusNotFound, ""
	}
	return &http.Response{
		StatusCode: statut,
		Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       io.NopCloser(strings.NewReader(corps)),
		Request:    requete,
	}, nil
}

func (r *reseauFactice) appels() []appelSortant {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]appelSortant(nil), r.vus...)
}

func (r *reseauFactice) appelsVers(hote string) []appelSortant {
	return versLHote(r.appels(), hote)
}

// robotsDemandes compte les requêtes de robots.txt émises.
func (r *reseauFactice) robotsDemandes() int {
	var lus int
	for _, appel := range r.appels() {
		if strings.HasSuffix(appel.url, "/robots.txt") {
			lus++
		}
	}
	return lus
}

// avecReseau branche le réseau factice sous recuperation, et non à sa place : la
// couture appelle le vrai chemin, transport excepté. Les options que l'ouvrier
// passe — cadence, robots retenus — sont donc celles de production, et ce que le
// test lit sont les requêtes réellement émises.
//
// C'est ce que le site factice ne peut pas montrer : il tient lieu de
// recuperation, et une doublure qui refait ce qu'on prétend vérifier ne prouve
// rien.
func avecReseau(t *testing.T, h *horlogeVirtuelle, corps map[string]string) *reseauFactice {
	t.Helper()

	r := &reseauFactice{horloge: h, corps: corps}
	avecRecuperateur(t, func(ctx context.Context, adresse string, choix ...recuperation.Option) (recuperation.Page, error) {
		return recuperation.Recupere(ctx, adresse, append(choix, recuperation.AvecTransport(r))...)
	})
	return r
}

// siteServi décrit ce que le réseau factice sert : le robots.txt donné pour
// chaque hôte visé, et une page de recette par URL.
func siteServi(robots string, urls ...string) map[string]string {
	corps := map[string]string{}
	for _, adresse := range urls {
		corps[adresse] = string(htmlDeRecette("Tarte de "+adresse, []string{"200 g de farine"}))
		corps["https://"+hoteDe(adresse)+"/robots.txt"] = robots
	}
	return corps
}

// --- Les pages servies ------------------------------------------------------

// htmlDeRecette fabrique une page qui publie la recette en JSON-LD. Passer par
// encoding/json plutôt que par un gabarit de chaîne : un titre à guillemets ne
// doit pas casser la fixture.
func htmlDeRecette(titre string, ingredients []string) []byte {
	return htmlDeRecetteIllustree(titre, "", ingredients)
}

// htmlDeRecetteIllustree fabrique la même page, qui publie en plus l'adresse
// d'une image. Une adresse vide n'écrit pas la propriété : une page sans image
// n'est pas une page dont l'image est la chaîne vide.
func htmlDeRecetteIllustree(titre, image string, ingredients []string) []byte {
	recette := map[string]any{
		"@context":           "https://schema.org",
		"@type":              "Recipe",
		"name":               titre,
		"recipeYield":        "4 personnes",
		"prepTime":           "PT20M",
		"cookTime":           "PT35M",
		"recipeIngredient":   ingredients,
		"recipeInstructions": "Mélanger, puis cuire.",
	}
	if image != "" {
		recette["image"] = image
	}

	bloc, err := json.Marshal(recette)
	if err != nil {
		panic(err)
	}
	return []byte(`<html><head><meta property="og:site_name" content="Le Site">` +
		`<script type="application/ld+json">` + string(bloc) + `</script></head><body></body></html>`)
}

// sertLaRecette rend la page d'une recette, la réponse venant de urlFinale.
func sertLaRecette(titre, urlFinale string, ingredients ...string) reponseDuSite {
	return sertLaPage(htmlDeRecette(titre, ingredients), urlFinale)
}

// sertLaRecetteIllustree rend la page d'une recette qui publie une image, à
// l'adresse donnée — absolue, ou relative à résoudre contre l'URL finale.
func sertLaRecetteIllustree(titre, urlFinale, image string, ingredients ...string) reponseDuSite {
	return sertLaPage(htmlDeRecetteIllustree(titre, image, ingredients), urlFinale)
}

// sertLaPage rend une page quelconque, telle que PATA-8 la ramènerait.
func sertLaPage(corps []byte, urlFinale string) reponseDuSite {
	return reponseDuSite{page: recuperation.Page{
		Corps:       corps,
		URLFinale:   urlFinale,
		TypeContenu: "text/html",
	}}
}

// echoueAvec rend l'échec nommé que PATA-8 ou PATA-7 rendrait.
func echoueAvec(err error) reponseDuSite {
	return reponseDuSite{err: err}
}

// recettesEn rend la table qui sert, à chaque URL, sa propre recette.
func recettesEn(urls []string) map[string]reponseDuSite {
	table := map[string]reponseDuSite{}
	for i, adresse := range urls {
		table[adresse] = sertLaRecette("Recette "+string(rune('A'+i)), adresse, "200 g de farine")
	}
	return table
}

// --- Montage ----------------------------------------------------------------

// atelierDeLOuvrier monte une base neuve, le compte qui lancera les lots, et
// l'ouvrier branché sur l'horloge virtuelle.
func atelierDeLOuvrier(t *testing.T) (core.App, *core.Record, *horlogeVirtuelle, *ouvrier) {
	t.Helper()

	app := baseNeuveAvec(t, analyseurDeTest(t))
	titulaire := compteParDefaut(t, app)
	h := nouvelleHorlogeVirtuelle()
	return app, titulaire, h, nouvelOuvrier(app, h)
}

// lotDe écrit la fournée que la page de saisie écrirait.
func lotDe(t *testing.T, app core.App, titulaire *core.Record, urls ...string) *core.Record {
	t.Helper()

	lot, _, err := creeLeLot(app, titulaire, urls, departDesTests)
	if err != nil {
		t.Fatalf("création du lot : %v", err)
	}
	return lot
}

// statutsDesLignes rend le statut de chaque ligne, dans l'ordre de position.
func statutsDesLignes(t *testing.T, app core.App, lot *core.Record) []string {
	t.Helper()

	var statuts []string
	for _, ligne := range lignesDuLot(t, app, lot) {
		statuts = append(statuts, ligne.GetString("status"))
	}
	return statuts
}

// relis relit un enregistrement : après un traitement, l'exemplaire que le
// test tient en main est périmé.
func relis(t *testing.T, app core.App, collection string, enregistrement *core.Record) *core.Record {
	t.Helper()

	relu, err := app.FindRecordById(collection, enregistrement.Id)
	if err != nil {
		t.Fatalf("relecture de %s/%s : %v", collection, enregistrement.Id, err)
	}
	return relu
}

// traite passe le lot à l'ouvrier et exige qu'il aille au bout.
func traite(t *testing.T, o *ouvrier, lot *core.Record) {
	t.Helper()

	if err := o.traiteLeLot(context.Background(), lot); err != nil {
		t.Fatalf("traitement du lot : %v", err)
	}
}

// --- Le cadencement ---------------------------------------------------------

// TestLOuvrierEspaceLesRequetesVersUnMemeHote : au plus une par seconde. C'est
// la politesse que le disclaimer de la page de saisie promet.
//
// Le compte se fait au transport, et non à la couture recuperePage : un appel de
// recuperation émet deux requêtes — le robots.txt de l'hôte, puis la page —, et
// la promesse porte sur celles-là. Comptée un cran plus haut, une paire partie
// dans le même instant passerait inaperçue.
func TestLOuvrierEspaceLesRequetesVersUnMemeHote(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{
		"https://a.example/1",
		"https://a.example/2",
		"https://a.example/3",
		"https://a.example/4",
	}
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", urls...))

	traite(t, o, lotDe(t, app, titulaire, urls...))

	appels := reseau.appels()
	// Un robots.txt, puis une requête par page : il est retenu pour la durée de
	// la fournée, et non redemandé à chaque page.
	if len(appels) != len(urls)+1 {
		t.Fatalf("%d requêtes émises, attendu %d — un robots.txt et %d pages :\n%v",
			len(appels), len(urls)+1, len(urls), appels)
	}
	if !strings.HasSuffix(appels[0].url, "/robots.txt") {
		t.Errorf("première requête vers %q, attendu le robots.txt", appels[0].url)
	}
	for i, ecart := range ecartsEntre(appels) {
		if ecart < delaiEntreRequetes {
			t.Errorf("requêtes %q et %q espacées de %v, attendu au moins %v",
				appels[i].url, appels[i+1].url, ecart, delaiEntreRequetes)
		}
	}
}

// TestLOuvrierMeneLesHotesDeFront : le cadencement d'un hôte ne retient pas
// les autres. La durée totale est celle du plus gros hôte, pas la somme.
func TestLOuvrierMeneLesHotesDeFront(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{
		"https://a.example/1", "https://b.example/1",
		"https://a.example/2", "https://b.example/2",
		"https://a.example/3", "https://b.example/3",
		"https://a.example/4", "https://b.example/4",
	}
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", urls...))

	traite(t, o, lotDe(t, app, titulaire, urls...))

	appels := reseau.appels()
	// Un robots.txt et quatre pages par hôte.
	if len(appels) != len(urls)+2 {
		t.Fatalf("%d requêtes émises, attendu %d", len(appels), len(urls)+2)
	}
	// Cinq requêtes par hôte, menées de front : quatre attentes d'une seconde,
	// et non les neuf qu'une file unique imposerait.
	attendue := 4 * delaiEntreRequetes
	if totale := depuisLeDepart(appels[len(appels)-1].instant); totale != attendue {
		t.Errorf("lot mené en %v, attendu %v (la somme des deux hôtes ferait %v)",
			totale, attendue, 9*delaiEntreRequetes)
	}
}

// TestLOuvrierBorneLesHotesMenesDeFront : filesMax est la seule borne du
// produit sur le nombre de requêtes sortantes simultanées. Une file est
// séquentielle — une requête, puis l'attente — donc compter les files menées
// de front, c'est compter les requêtes en vol.
//
// Le test fige aussi ce que le découpage en vagues coûte : au-delà de filesMax
// hôtes, le suivant attend que la vague en cours soit finie. Le critère
// « durée totale égale à celle du plus gros hôte » ne vaut donc que jusqu'à
// filesMax hôtes, et c'est assumé.
func TestLOuvrierBorneLesHotesMenesDeFront(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)

	// Un hôte lent en tête de la première vague, filesMax-1 hôtes d'une seule
	// URL pour la remplir, et un hôte de trop juste derrière.
	lent := []string{"https://lent.example/1", "https://lent.example/2", "https://lent.example/3"}
	urls := append([]string{}, lent...)
	for rang := 1; rang < filesMax; rang++ {
		urls = append(urls, fmt.Sprintf("https://h%d.example/1", rang))
	}
	urls = append(urls, "https://detrop.example/1")

	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", urls...))
	traite(t, o, lotDe(t, app, titulaire, urls...))

	// Un robots.txt par hôte, plus une page par URL.
	attendues := len(urls) + filesMax + 1
	if len(reseau.appels()) != attendues {
		t.Fatalf("%d requêtes émises, attendu %d", len(reseau.appels()), attendues)
	}
	if sommet := horloge.sommetDesFiles(); sommet > filesMax {
		t.Errorf("%d hôtes menés de front, attendu au plus %d : la borne ne tient pas", sommet, filesMax)
	}

	// L'hôte de trop n'a pas démarré avant que l'hôte lent de la première vague
	// ait fini son robots.txt et ses trois URLs, soit trois secondes de cadence.
	enTrop := reseau.appelsVers("detrop.example")
	if len(enTrop) != 2 {
		t.Fatalf("%d requêtes vers l'hôte de trop, attendu 2 — son robots.txt et sa page", len(enTrop))
	}
	if attente := depuisLeDepart(enTrop[0].instant); attente != 3*delaiEntreRequetes {
		t.Errorf("l'hôte au-delà de la borne a démarré à %v, attendu %v : la vague suivante ne devrait "+
			"pas démarrer avant que la précédente soit finie", attente, 3*delaiEntreRequetes)
	}
}

// TestLOuvrierSuitLeCrawlDelayAnnonce : quand l'hôte demande plus que notre
// seconde, c'est lui qui décide — et pour lui seul.
func TestLOuvrierSuitLeCrawlDelayAnnonce(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	patient := []string{"https://lent.example/1", "https://lent.example/2", "https://lent.example/3"}
	ordinaire := []string{"https://vif.example/1", "https://vif.example/2", "https://vif.example/3"}

	corps := siteServi("User-agent: *\nCrawl-delay: 5\n", patient...)
	for adresse, contenu := range siteServi("User-agent: *\nDisallow: /prive\n", ordinaire...) {
		corps[adresse] = contenu
	}
	reseau := avecReseau(t, horloge, corps)

	traite(t, o, lotDe(t, app, titulaire, append(append([]string{}, patient...), ordinaire...)...))

	// Le robots.txt en tête, puis les trois pages : ce sont les écarts entre
	// pages qui disent le rythme que l'hôte impose à la suite.
	ecartsEntreLesPages := func(hote string) []time.Duration {
		appels := reseau.appelsVers(hote)
		if len(appels) != 4 {
			t.Fatalf("%d requêtes vers %s, attendu 4 — son robots.txt et trois pages", len(appels), hote)
		}
		return ecartsEntre(appels[1:])
	}

	for _, ecart := range ecartsEntreLesPages("lent.example") {
		if ecart != 5*time.Second {
			t.Errorf("écart de %v vers l'hôte qui annonce Crawl-delay: 5, attendu 5s", ecart)
		}
	}
	for _, ecart := range ecartsEntreLesPages("vif.example") {
		if ecart != delaiEntreRequetes {
			t.Errorf("écart de %v vers l'hôte qui n'annonce rien, attendu %v", ecart, delaiEntreRequetes)
		}
	}
}

// TestLeCrawlDelayVautDesLaPremierePage : le délai annoncé se lit dans le
// robots.txt, donc avant la page. Rapporté après l'appel, il ne vaudrait qu'à
// partir de la deuxième — et la première partirait à notre rythme, pas à celui
// du site, qui vient pourtant de l'écrire.
func TestLeCrawlDelayVautDesLaPremierePage(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const adresse = "https://lent.example/1"
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nCrawl-delay: 5\n", adresse))

	traite(t, o, lotDe(t, app, titulaire, adresse))

	appels := reseau.appels()
	if len(appels) != 2 {
		t.Fatalf("%d requêtes émises, attendu 2 :\n%v", len(appels), appels)
	}
	if ecart := appels[1].instant.Sub(appels[0].instant); ecart != 5*time.Second {
		t.Errorf("page demandée %v après le robots.txt qui annonce Crawl-delay: 5, attendu 5s", ecart)
	}
}

// TestChaqueFourneeRelitLeRobots : le robots.txt est retenu pour la fournée, et
// pour elle seule.
//
// Retenu plus longtemps — porté par l'ouvrier, donc par le service —, un site
// qui se ferme entre deux lots continuerait d'être récolté jusqu'au redémarrage
// du processus, quand la page de saisie promet le contraire.
func TestChaqueFourneeRelitLeRobots(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const premiere, seconde = "https://a.example/1", "https://a.example/2"
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", premiere, seconde))

	traite(t, o, lotDe(t, app, titulaire, premiere))
	traite(t, o, lotDe(t, app, titulaire, seconde))

	if lus := reseau.robotsDemandes(); lus != 2 {
		t.Errorf("%d robots.txt demandés pour deux fournées, attendu 2 : la seconde a lu ce que "+
			"la première avait retenu", lus)
	}
}

// --- La reprise -------------------------------------------------------------

// TestLOuvrierDemarreAvecLeServeurEtSArreteAvecLui : brancheLOuvrier est le
// seul chemin par lequel l'ouvrier tourne en production. Sans ce test, retirer
// son appel de main() laisserait la suite entièrement verte.
//
// L'arrêt est la seconde moitié, et c'est celle qui n'a rien pour la retenir :
// OnTerminate ne doit rendre la main qu'une fois la ligne en cours rendue à la
// file, sur une base encore ouverte. C'est cet état-là que la reprise du
// démarrage suivant attend.
func TestLOuvrierDemarreAvecLeServeurEtSArreteAvecLui(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	titulaire := compteParDefaut(t, app)
	lot := lotDe(t, app, titulaire, "https://a.example/1")

	partie := make(chan struct{}, 1)
	avecRecuperateur(t, func(ctx context.Context, _ string, _ ...recuperation.Option) (recuperation.Page, error) {
		select {
		case partie <- struct{}{}:
		default:
		}
		<-ctx.Done()
		// Rendre la ligne à la file prend un instant. Sans l'attente
		// d'OnTerminate, le test lirait la base avant cette écriture-là.
		time.Sleep(50 * time.Millisecond)
		return recuperation.Page{}, ctx.Err()
	})

	brancheLOuvrier(app)
	// Un test qui échoue avant l'arrêt laisserait l'ouvrier tourner sur une
	// base que le nettoyage referme. Arrêter deux fois est sans effet.
	t.Cleanup(func() { _ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app}) })

	if err := app.OnServe().Trigger(&core.ServeEvent{App: app}); err != nil {
		t.Fatalf("démarrage du serveur : %v", err)
	}
	select {
	case <-partie:
	case <-time.After(10 * time.Second):
		t.Fatal("aucune requête n'est partie : l'ouvrier n'a pas démarré avec le serveur")
	}

	if err := app.OnTerminate().Trigger(&core.TerminateEvent{App: app}); err != nil {
		t.Fatalf("arrêt du serveur : %v", err)
	}

	if statut := lignesDuLot(t, app, lot)[0].GetString("status"); statut != "a_faire" {
		t.Errorf("ligne en %q quand OnTerminate a rendu la main, attendu « a_faire » : "+
			"l'arrêt n'a pas attendu l'ouvrier", statut)
	}
}

// TestUneLigneRestEeEnCoursEstReprise : un processus arrêté au mauvais moment
// laisse une ligne « en_cours » que personne ne réclamerait plus.
func TestUneLigneResteeEnCoursEstReprise(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{"https://a.example/1", "https://a.example/2"}
	site := avecSite(t, horloge, recettesEn(urls))
	lot := lotDe(t, app, titulaire, urls...)

	interrompue := lignesDuLot(t, app, lot)[0]
	interrompue.Set("status", "en_cours")
	if err := app.Save(interrompue); err != nil {
		t.Fatalf("mise en cours de la ligne : %v", err)
	}

	if err := o.reprend(context.Background()); err != nil {
		t.Fatalf("reprise : %v", err)
	}

	if statuts := statutsDesLignes(t, app, lot); statuts[0] != "importee" {
		t.Errorf("ligne interrompue en %q, attendu « importee » : elle a été abandonnée", statuts[0])
	}
	if len(site.appelsVers("a.example")) != 2 {
		t.Errorf("%d requêtes émises, attendu 2 : la ligne interrompue n'a pas été rejouée",
			len(site.appelsVers("a.example")))
	}
}

// TestUnLotRepriseNeRejoueAucuneURLDejaTraitee : la somme des appels avant et
// après la reprise égale le nombre d'URLs. Un lot de 500 URLs coupé au milieu
// ne recommence pas.
func TestUnLotRepriseNeRejoueAucuneURLDejaTraitee(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{
		"https://a.example/1",
		"https://a.example/2",
		"https://a.example/3",
		"https://a.example/4",
	}

	ctx, annule := context.WithCancel(context.Background())
	table := recettesEn(urls)
	// L'arrêt survient une fois la deuxième page rendue : la ligne est menée à
	// son terme, et c'est la suivante qui trouve le contexte coupé.
	table[urls[1]] = reponseDuSite{page: table[urls[1]].page, apres: annule}
	site := avecSite(t, horloge, table)

	lot := lotDe(t, app, titulaire, urls...)
	if err := o.traiteLeLot(ctx, lot); err == nil {
		t.Fatal("le lot interrompu s'est terminé sans erreur")
	}

	avant := len(site.appels())
	if avant != 2 {
		t.Fatalf("%d requêtes avant l'arrêt, attendu 2", avant)
	}

	if err := o.reprend(context.Background()); err != nil {
		t.Fatalf("reprise : %v", err)
	}

	if total := len(site.appels()); total != len(urls) {
		t.Errorf("%d requêtes en tout (%d avant l'arrêt), attendu %d : des URLs ont été rejouées",
			total, avant, len(urls))
	}
	if n := compte(t, app, "recipes"); n != len(urls) {
		t.Errorf("%d recettes créées, attendu %d", n, len(urls))
	}
}

// TestLArretRendLaLigneEnCoursAFaire : le contexte coupé pendant la
// récupération ne laisse ni ligne en cours, ni lot terminé — c'est ce que la
// reprise attend au démarrage suivant.
func TestLArretRendLaLigneEnCoursAFaire(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{"https://a.example/1", "https://a.example/2"}

	ctx, annule := context.WithCancel(context.Background())
	table := recettesEn(urls)
	table[urls[0]] = reponseDuSite{
		err:   context.Canceled,
		apres: annule,
	}
	avecSite(t, horloge, table)

	lot := lotDe(t, app, titulaire, urls...)
	if err := o.traiteLeLot(ctx, lot); err == nil {
		t.Fatal("le lot interrompu s'est terminé sans erreur")
	}

	for rang, statut := range statutsDesLignes(t, app, lot) {
		if statut != "a_faire" {
			t.Errorf("ligne %d en %q après l'arrêt, attendu « a_faire »", rang+1, statut)
		}
	}
	if statut := relis(t, app, "imports", lot).GetString("status"); statut != "en_cours" {
		t.Errorf("lot en %q après l'arrêt, attendu « en_cours »", statut)
	}
}

// TestUnLotDontLeCompteAEteSupprimeNeBloquePlusLOuvrier : le constat de
// PATA-45, monté tel quel. Un lot dont le titulaire a été supprimé n'est plus
// vu par l'ouvrier — il n'existe plus, et la reprise passe sans rien laisser
// d'ouvert.
//
// Les deux premières exigences — la reprise ne rend pas d'erreur, elle ne
// laisse aucun lot « en_cours » — tiennent déjà sans la cascade, et c'est un
// écart avec l'énoncé du constat : PocketBase 0.39.11 ne laisse pas de
// relation pendante en base, il vide created_by par un SaveNoValidate
// (core/record_model.go, deleteRefRecords). Le lot relu se clôt donc
// normalement. L'erreur « Failed to find all relation records with the
// provided ids » ne frappe qu'un exemplaire lu *avant* la suppression, que
// l'ouvrier garde en main le temps d'un tour de lot. C'est la troisième
// exigence — le lot n'existe plus du tout — qui porte la cascade : elle rougit
// si on la retire.
func TestUnLotDontLeCompteAEteSupprimeNeBloquePlusLOuvrier(t *testing.T) {
	app, titulaire, _, o := atelierDeLOuvrier(t)
	lot := lotDe(t, app, titulaire, "https://a.example/1")

	// La ligne est déjà menée à son terme : il ne reste que la clôture, qui
	// est l'endroit exact où le constat échouait. Aucun site n'est monté,
	// puisque plus rien n'est à faire.
	ligne := lignesDuLot(t, app, lot)[0]
	ligne.Set("status", "importee")
	if err := app.Save(ligne); err != nil {
		t.Fatalf("ligne menée à son terme : %v", err)
	}

	if err := app.Delete(titulaire); err != nil {
		t.Fatalf("suppression du compte : %v", err)
	}

	if err := o.reprend(context.Background()); err != nil {
		t.Fatalf("reprise après la suppression du compte : %v", err)
	}

	restants, err := app.CountRecords("imports", dbx.HashExp{"status": "en_cours"})
	if err != nil {
		t.Fatal(err)
	}
	if restants != 0 {
		t.Errorf("%d lot(s) encore « en_cours » après la reprise : l'ouvrier les rebalaiera sans fin",
			restants)
	}
	if _, err := app.FindRecordById("imports", lot.Id); err == nil {
		t.Error("le lot survit à la suppression de son titulaire : l'ouvrier le reverra, " +
			"et un exemplaire lu avant la suppression ne se sauvegardera plus")
	}
}

// --- La déduplication -------------------------------------------------------

// TestReimporterLeMemeLotNeCreeAucunDoublon : la fournée est refaite, le
// carnet ne bouge pas.
func TestReimporterLeMemeLotNeCreeAucunDoublon(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{"https://a.example/1", "https://a.example/2"}
	avecSite(t, horloge, recettesEn(urls))

	traite(t, o, lotDe(t, app, titulaire, urls...))
	apresLePremier := compte(t, app, "recipes")
	if apresLePremier != len(urls) {
		t.Fatalf("%d recettes après le premier passage, attendu %d", apresLePremier, len(urls))
	}

	second := lotDe(t, app, titulaire, urls...)
	traite(t, o, second)

	if apresLeSecond := compte(t, app, "recipes"); apresLeSecond != apresLePremier {
		t.Errorf("%d recettes après le second passage, attendu %d", apresLeSecond, apresLePremier)
	}
	for rang, statut := range statutsDesLignes(t, app, second) {
		if statut != "deja_presente" {
			t.Errorf("ligne %d du second lot en %q, attendu « deja_presente »", rang+1, statut)
		}
	}
}

// TestUneURLDejaPresenteNEmetAucuneRequete : la déduplication porte d'abord
// sur l'URL soumise, avant tout appel. Le piège fait échouer le test si une
// requête part.
func TestUneURLDejaPresenteNEmetAucuneRequete(t *testing.T) {
	app, titulaire, _, o := atelierDeLOuvrier(t)
	const adresse = "https://a.example/deja"

	connue := creeRecette(t, app, recetteVoulue{titre: "Déjà là"})
	connue.Set("source_url", adresse)
	if err := app.Save(connue); err != nil {
		t.Fatalf("pose de la source : %v", err)
	}

	reseauPiege(t)
	lot := lotDe(t, app, titulaire, adresse)
	traite(t, o, lot)

	if statut := statutsDesLignes(t, app, lot)[0]; statut != "deja_presente" {
		t.Errorf("ligne en %q, attendu « deja_presente »", statut)
	}
}

// TestDeuxURLsVersLaMemePageNeCreentQuUneRecette : la déduplication porte
// aussi sur l'URL finale — deux adresses peuvent rediriger vers la même page.
//
// Le cas à deux hôtes n'est pas une variante décorative : www.site.fr et
// site.fr sont deux hôtes, donc deux files menées de front, et la lecture de
// l'URL finale y précède l'écriture de la recette dans deux goroutines à la
// fois. Le cas à un seul hôte est séquentiel par construction : il couvre
// justement celui où le code ne peut pas se tromper.
func TestDeuxURLsVersLaMemePageNeCreentQuUneRecette(t *testing.T) {
	cas := []struct {
		nom  string
		urls []string
		// ordonne dit si l'on sait laquelle des deux lignes écrit la recette.
		// Sur un seul hôte, c'est la première ; sur deux files de front, ce
		// n'est pas fixé, et ce n'est pas ce qui est promis.
		ordonne bool
	}{
		{"un seul hôte", []string{"https://a.example/court", "https://a.example/long"}, true},
		{"deux hôtes menés de front", []string{"https://www.site.fr/tarte", "https://site.fr/tarte"}, false},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, titulaire, horloge, o := atelierDeLOuvrier(t)
			const finale = "https://site.fr/canonique"

			avecSite(t, horloge, map[string]reponseDuSite{
				c.urls[0]: sertLaRecette("Tarte", finale, "200 g de farine"),
				c.urls[1]: sertLaRecette("Tarte", finale, "200 g de farine"),
			})

			lot := lotDe(t, app, titulaire, c.urls...)
			traite(t, o, lot)

			if n := compte(t, app, "recipes"); n != 1 {
				t.Errorf("%d recettes créées pour une seule page finale, attendu 1", n)
			}

			statuts := statutsDesLignes(t, app, lot)
			attendus := []string{"importee", "deja_presente"}
			if !c.ordonne {
				statuts = slices.Sorted(slices.Values(statuts))
				slices.Sort(attendus)
			}
			if !slices.Equal(statuts, attendus) {
				t.Errorf("statuts des lignes %v, attendu %v", statuts, attendus)
			}
		})
	}
}

// --- Ce que la recette créée porte ------------------------------------------

// TestLaRecetteImporteePorteSonAuteurEtSaSource : l'auteur est le compte qui a
// lancé le lot, et la source l'URL du dernier saut.
func TestLaRecetteImporteePorteSonAuteurEtSaSource(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const soumise = "https://a.example/court"
	const finale = "https://a.example/canonique"

	avecSite(t, horloge, map[string]reponseDuSite{
		soumise: sertLaRecette("Tarte aux pommes", finale, "200 g de farine"),
	})

	lot := lotDe(t, app, titulaire, soumise)
	traite(t, o, lot)

	ligne := lignesDuLot(t, app, lot)[0]
	if statut := ligne.GetString("status"); statut != "importee" {
		t.Fatalf("ligne en %q, attendu « importee »", statut)
	}
	recette, err := app.FindRecordById("recipes", ligne.GetString("recipe"))
	if err != nil {
		t.Fatalf("la ligne ne pointe aucune recette : %v", err)
	}

	if auteur := recette.GetString(champAuteur); auteur != titulaire.Id {
		t.Errorf("created_by %q, attendu %q", auteur, titulaire.Id)
	}
	if source := recette.GetString("source_url"); source != finale {
		t.Errorf("source_url %q, attendu %q — c'est l'URL finale qui désigne la recette", source, finale)
	}
	if titre := recette.GetString("title"); titre != "Tarte aux pommes" {
		t.Errorf("titre %q, attendu %q", titre, "Tarte aux pommes")
	}
}

// TestLesIngredientsImportesSontAnalyses : la ligne brute est écrite, et le
// hook de PATA-6 en tire les cinq champs. Rien n'est recopié du parser ici.
func TestLesIngredientsImportesSontAnalyses(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const adresse = "https://a.example/tarte"

	avecSite(t, horloge, map[string]reponseDuSite{
		adresse: sertLaRecette("Tarte", adresse, "environ 200 g de farine"),
	})

	lot := lotDe(t, app, titulaire, adresse)
	traite(t, o, lot)

	lignes, err := app.FindAllRecords("ingredients")
	if err != nil {
		t.Fatalf("lecture des ingrédients : %v", err)
	}
	if len(lignes) != 1 {
		t.Fatalf("%d lignes d'ingrédient, attendu 1", len(lignes))
	}
	ligne := lignes[0]
	if brut := ligne.GetString("raw"); brut != "environ 200 g de farine" {
		t.Errorf("raw %q, attendu %q", brut, "environ 200 g de farine")
	}
	if aliment := ligne.GetString("food"); aliment != "farine" {
		t.Errorf("food %q, attendu %q : le hook de lecture n'a pas tourné", aliment, "farine")
	}
	if quantite := ligne.GetFloat("quantity"); quantite != 200 {
		t.Errorf("quantity %v, attendu 200", quantite)
	}
	if unite := ligne.GetString("unit"); unite != "g" {
		t.Errorf("unit %q, attendu %q", unite, "g")
	}
}

// TestUnLotSansAuteurNeCreeAucuneRecette : une recette sans auteur ne serait
// supprimable par personne — la règle de recipes exige
// created_by = @request.auth.id, qu'aucune session ne satisfait. Mieux vaut une
// ligne en échec, que le rapport de la fournée nomme, qu'une recette orpheline
// créée en silence.
func TestUnLotSansAuteurNeCreeAucuneRecette(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const soumise = "https://a.example/tarte"
	avecSite(t, horloge, map[string]reponseDuSite{
		soumise: sertLaRecette("Tarte", soumise, "200 g de farine"),
	})

	// Un lot sans auteur : c'est ce que laisse un lot lancé depuis un compte
	// superuser, dont l'identifiant n'est pas celui d'un users — poseLAuteur
	// ne pose alors rien (acces.go). Le champ n'est pas obligatoire, le lot
	// existe et attend son tour comme les autres.
	lot := lotDe(t, app, titulaire, soumise)
	lot.Set(champAuteur, "")
	if err := app.Save(lot); err != nil {
		t.Fatalf("lot sans auteur : %v", err)
	}

	traite(t, o, lot)

	if n := compte(t, app, "recipes"); n != 0 {
		t.Errorf("%d recettes créées, attendu 0 : aucune ne doit naître sans auteur", n)
	}
	ligne := lignesDuLot(t, app, lot)[0]
	if statut := ligne.GetString("status"); statut != statutEchec {
		t.Errorf("ligne en %q, attendu %q", statut, statutEchec)
	}
	if cause := ligne.GetString("cause"); cause != causeEnregistrement {
		t.Errorf("cause %q, attendu %q", cause, causeEnregistrement)
	}
	if relu := relis(t, app, "imports", lot); relu.GetString("status") != statutTermine {
		t.Errorf("lot en %q, attendu %q : une ligne sans sort le laisserait en cours pour toujours",
			relu.GetString("status"), statutTermine)
	}
}

// --- Le tag de la fournée ---------------------------------------------------

// TestLesRecettesDUnLotPortentSonTag : le tag rend la fournée retrouvable, et
// seules les recettes qui ont abouti le portent.
func TestLesRecettesDUnLotPortentSonTag(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{"https://a.example/1", "https://a.example/2", "https://a.example/3"}

	table := recettesEn(urls)
	table[urls[1]] = echoueAvec(&recuperation.Erreur{Cause: recuperation.RefusHTTP, Code: 403, URL: urls[1]})
	avecSite(t, horloge, table)

	lot := lotDe(t, app, titulaire, urls...)
	traite(t, o, lot)

	tag := relis(t, app, "imports", lot).GetString("tag")
	if tag == "" {
		t.Fatal("le lot ne porte aucun tag")
	}

	recettes, err := app.FindAllRecords("recipes")
	if err != nil {
		t.Fatalf("lecture des recettes : %v", err)
	}
	if len(recettes) != 2 {
		t.Fatalf("%d recettes créées, attendu 2", len(recettes))
	}
	for _, recette := range recettes {
		tags := recette.GetStringSlice("tags")
		if len(tags) != 1 || tags[0] != tag {
			t.Errorf("recette %q : tags %v, attendu [%s]", recette.GetString("title"), tags, tag)
		}
	}
}

// TestLesRecettesDUnSecondLotNePortentPasLeTagDuPremier : deux fournées, deux
// tags, et aucun mélange.
func TestLesRecettesDUnSecondLotNePortentPasLeTagDuPremier(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	premieres := []string{"https://a.example/1"}
	secondes := []string{"https://a.example/2"}
	avecSite(t, horloge, recettesEn(append(append([]string{}, premieres...), secondes...)))

	premier := lotDe(t, app, titulaire, premieres...)
	traite(t, o, premier)
	second := lotDe(t, app, titulaire, secondes...)
	traite(t, o, second)

	tagDuPremier := relis(t, app, "imports", premier).GetString("tag")
	tagDuSecond := relis(t, app, "imports", second).GetString("tag")
	if tagDuPremier == tagDuSecond {
		t.Fatal("les deux lots partagent le même tag")
	}

	ligne := lignesDuLot(t, app, second)[0]
	recette, err := app.FindRecordById("recipes", ligne.GetString("recipe"))
	if err != nil {
		t.Fatalf("recette du second lot : %v", err)
	}
	for _, pose := range recette.GetStringSlice("tags") {
		if pose == tagDuPremier {
			t.Errorf("la recette du second lot porte le tag du premier (%s)", tagDuPremier)
		}
	}
}

// TestLeTagDuLotSAjouteAuxTagsDejaPoses : un ajout, et non un Set d'une liste
// d'un seul élément. Aujourd'hui une recette importée n'arrive avec aucun tag ;
// le jour où la correspondance des champs en produira, ce détail sera la
// différence entre garder et perdre.
func TestLeTagDuLotSAjouteAuxTagsDejaPoses(t *testing.T) {
	app, titulaire, _, _ := atelierDeLOuvrier(t)

	lot := lotDe(t, app, titulaire, "https://a.example/1")
	tagDuLot := lot.GetString("tag")
	dejaPose := creeTags(t, app, []string{"dessert"})[0]

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("tags", []string{dejaPose.Id})

	page := recuperation.Page{Corps: htmlDeRecette("Tarte", nil), URLFinale: "https://a.example/1"}
	trouvee, err := jsonld.Extraire(page.Corps)
	if err != nil {
		t.Fatalf("extraction de la fixture : %v", err)
	}
	if err := creeLaRecetteImportee(app, recette, lot, page, trouvee); err != nil {
		t.Fatalf("création de la recette : %v", err)
	}

	tags := relis(t, app, "recipes", recette).GetStringSlice("tags")
	if len(tags) != 2 {
		t.Fatalf("%d tags posés (%v), attendu 2 : le tag du lot a remplacé celui de la recette", len(tags), tags)
	}
	for _, attendu := range []string{dejaPose.Id, tagDuLot} {
		trouve := false
		for _, pose := range tags {
			trouve = trouve || pose == attendu
		}
		if !trouve {
			t.Errorf("tag %s absent de %v", attendu, tags)
		}
	}
}

// --- Les causes d'échec -----------------------------------------------------

// TestLesCausesDEchecSontEnregistrees : les six causes de recuperation et les
// quatre de jsonld, écrites telles quelles, avec le code HTTP quand l'échec en
// porte un. C'est ce que le rapport de la sous-tâche 4 lira.
func TestLesCausesDEchecSontEnregistrees(t *testing.T) {
	const adresse = "https://a.example/tarte"

	refus := func(cause string, code int) reponseDuSite {
		return echoueAvec(&recuperation.Erreur{Cause: cause, Code: code, URL: adresse})
	}
	bloc := func(contenu string) reponseDuSite {
		return sertLaPage([]byte(`<html><head><script type="application/ld+json">`+
			contenu+`</script></head><body></body></html>`), adresse)
	}

	cas := []struct {
		nom     string
		reponse reponseDuSite
		cause   string
		code    int
	}{
		{"injoignable", refus(recuperation.Injoignable, 0), recuperation.Injoignable, 0},
		{"refus http", refus(recuperation.RefusHTTP, 403), recuperation.RefusHTTP, 403},
		{"refusée par politique", refus(recuperation.RefuseeParPolitique, 0), recuperation.RefuseeParPolitique, 0},
		{"robots interdit", refus(recuperation.RobotsInterdit, 0), recuperation.RobotsInterdit, 0},
		{"délai dépassé", refus(recuperation.DelaiDepasse, 0), recuperation.DelaiDepasse, 0},
		{"taille max", refus(recuperation.TailleMax, 0), recuperation.TailleMax, 0},
		{"aucun balisage", sertLaPage([]byte(`<html><body>rien</body></html>`), adresse), jsonld.AucunBalisage, 0},
		{"json invalide", bloc(`{"@type":`), jsonld.JSONInvalide, 0},
		{"sans recette", bloc(`{"@context":"https://schema.org","@type":"Person","name":"Paul"}`), jsonld.SansRecette, 0},
		{"titre absent", bloc(`{"@context":"https://schema.org","@type":"Recipe","recipeYield":"4"}`), jsonld.TitreAbsent, 0},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, titulaire, horloge, o := atelierDeLOuvrier(t)
			avecSite(t, horloge, map[string]reponseDuSite{adresse: c.reponse})

			lot := lotDe(t, app, titulaire, adresse)
			traite(t, o, lot)

			ligne := lignesDuLot(t, app, lot)[0]
			if statut := ligne.GetString("status"); statut != "echec" {
				t.Fatalf("ligne en %q, attendu « echec »", statut)
			}
			if cause := ligne.GetString("cause"); cause != c.cause {
				t.Errorf("cause %q, attendu %q", cause, c.cause)
			}
			if code := ligne.GetInt("code"); code != c.code {
				t.Errorf("code %d, attendu %d", code, c.code)
			}
			if n := compte(t, app, "recipes"); n != 0 {
				t.Errorf("%d recettes créées pour une URL en échec, attendu 0", n)
			}
		})
	}
}

// baseQuiRefuseUneSource fait échouer la recherche d'une source précise, et
// rien d'autre. C'est la panne qu'aucun site ne provoque : une base
// indisponible, un disque plein, un schéma en cours de migration.
type baseQuiRefuseUneSource struct {
	core.App
	adresse string
}

var errBaseIndisponible = errors.New("base indisponible")

func (b baseQuiRefuseUneSource) FindFirstRecordByFilter(
	collection any,
	filtre string,
	params ...dbx.Params,
) (*core.Record, error) {
	for _, jeu := range params {
		if jeu["url"] == b.adresse {
			return nil, errBaseIndisponible
		}
	}
	return b.App.FindFirstRecordByFilter(collection, filtre, params...)
}

// TestUnePanneDeLectureDonneUnSortDefinitifALaLigne : une ligne doit toujours
// finir par un sort définitif, y compris quand c'est notre côté qui lâche.
//
// Laissée en cours, personne ne la réclamerait plus : l'ouvrier ne prend que
// ce qui est à faire, et les lignes interrompues ne sont rendues qu'au
// démarrage. Le lot resterait en cours pour toujours, rebalayé toutes les
// secondes jusqu'au prochain redémarrage du processus.
func TestUnePanneDeLectureDonneUnSortDefinitifALaLigne(t *testing.T) {
	const soumise = "https://a.example/court"
	const finale = "https://a.example/canonique"

	cas := []struct {
		nom     string
		refusee string
	}{
		// La déduplication porte aux deux bouts, et les deux lectures peuvent
		// échouer : avant l'appel sur l'adresse soumise, après lui sur l'URL
		// finale.
		{"avant l'appel", soumise},
		{"après l'appel", finale},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, titulaire, horloge, _ := atelierDeLOuvrier(t)
			avecSite(t, horloge, map[string]reponseDuSite{
				soumise: sertLaRecette("Tarte", finale, "200 g de farine"),
			})
			o := nouvelOuvrier(baseQuiRefuseUneSource{App: app, adresse: c.refusee}, horloge)

			lot := lotDe(t, app, titulaire, soumise)
			traite(t, o, lot)

			ligne := lignesDuLot(t, app, lot)[0]
			if statut := ligne.GetString("status"); statut != "echec" {
				t.Errorf("ligne en %q, attendu « echec » : une panne de notre côté ne lui a donné aucun sort", statut)
			}
			if cause := ligne.GetString("cause"); cause != causeEnregistrement {
				t.Errorf("cause %q, attendu %q", cause, causeEnregistrement)
			}
			if statut := relis(t, app, "imports", lot).GetString("status"); statut != "termine" {
				t.Errorf("lot en %q, attendu « termine » : une ligne sans sort le retient à jamais", statut)
			}
			if n := compte(t, app, "recipes"); n != 0 {
				t.Errorf("%d recettes créées, attendu 0", n)
			}
		})
	}
}

// --- La clôture du lot ------------------------------------------------------

// TestUnLotEntierementTraitePasseEnTermine : quel que soit le sort de ses
// lignes. Un lot dont chaque ligne a un sort définitif est terminé.
func TestUnLotEntierementTraitePasseEnTermine(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	urls := []string{"https://a.example/1", "https://a.example/2"}

	table := recettesEn(urls)
	table[urls[1]] = echoueAvec(&recuperation.Erreur{Cause: recuperation.Injoignable, URL: urls[1]})
	avecSite(t, horloge, table)

	lot := lotDe(t, app, titulaire, urls...)
	traite(t, o, lot)

	if statut := relis(t, app, "imports", lot).GetString("status"); statut != "termine" {
		t.Errorf("lot en %q, attendu « termine »", statut)
	}
	for rang, statut := range statutsDesLignes(t, app, lot) {
		if statut == "a_faire" || statut == "en_cours" {
			t.Errorf("ligne %d restée en %q dans un lot terminé", rang+1, statut)
		}
	}
}

// --- L'image de la fournée --------------------------------------------------
//
// Le lot illustre ses recettes comme l'import unitaire, et par le même chemin :
// adresseDeLImage résout, imageDistante télécharge, et toute la politique de
// recuperation s'applique. Ce que le lot ajoute est le placement — après la
// transaction, hors du verrou de création — et le portage de ses deux options
// jusqu'à l'image.

// laRecetteDeLaLigne rend la recette qu'une ligne importée désigne.
func laRecetteDeLaLigne(t *testing.T, app core.App, ligne *core.Record) *core.Record {
	t.Helper()

	if statut := ligne.GetString("status"); statut != statutImportee {
		t.Fatalf("ligne en %q, attendu %q", statut, statutImportee)
	}
	recette, err := app.FindRecordById("recipes", ligne.GetString("recipe"))
	if err != nil {
		t.Fatalf("la ligne ne pointe aucune recette : %v", err)
	}
	return recette
}

// exigeRecetteSansImageEtLigneImportee dit le sort d'une image qui n'a pas
// abouti : la recette est là, sans fichier, la ligne compte comme importée, et
// le rapport ne la range pas en échec.
func exigeRecetteSansImageEtLigneImportee(t *testing.T, app core.App, lignes []*core.Record) {
	t.Helper()

	recette := laRecetteDeLaLigne(t, app, lignes[0])
	if nom := recette.GetString("image"); nom != "" {
		t.Errorf("image %q attachée, aucune attendue", nom)
	}
	exigeStockageVide(t, app)

	comptes, _, echecs := leRapport(lignes)
	if len(echecs) != 0 {
		t.Errorf("%d ligne(s) en échec au rapport, aucune attendue : %v", len(echecs), echecs)
	}
	for _, compte := range comptes {
		if compte.Libelle == "Importées" && compte.Compte != len(lignes) {
			t.Errorf("%d importées au rapport, attendu %d", compte.Compte, len(lignes))
		}
	}
}

// TestUneRecetteDeFourneeRecoitLImageDeLaPage : sans téléchargement, l'adresse
// trouvée dans le balisage est perdue à l'enregistrement — le schéma n'a pas de
// champ pour elle —, et rouvrir la fiche ne la propose pas davantage.
func TestUneRecetteDeFourneeRecoitLImageDeLaPage(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const adresse = "https://a.example/tarte"
	octets := pngDeTest(t)

	serveur := serveurDImages(t, sertLesOctets(octets, "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))
	avecSite(t, horloge, map[string]reponseDuSite{
		adresse: sertLaRecetteIllustree("Tarte", adresse, serveur.URL+"/photo.png", "200 g de farine"),
	})

	lot := lotDe(t, app, titulaire, adresse)
	traite(t, o, lot)

	recette := laRecetteDeLaLigne(t, app, lignesDuLot(t, app, lot)[0])
	if stocke := fichierStocke(t, app, recette); !bytes.Equal(stocke, octets) {
		t.Errorf("%d octets stockés, les %d octets servis attendus", len(stocke), len(octets))
	}
}

// TestUneImageRelativeSeResoutContreLURLFinale : beaucoup de sites n'écrivent
// que le chemin de leurs images. C'est l'URL du dernier saut qui la résout —
// celle qui désigne déjà la recette dans source_url —, et non l'adresse
// soumise : après une redirection qui change d'hôte, celle-ci viserait à côté.
func TestUneImageRelativeSeResoutContreLURLFinale(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const soumise = "https://a.example/court"
	octets := pngDeTest(t)

	serveur := serveurDImages(t, sertLesOctets(octets, "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))
	avecSite(t, horloge, map[string]reponseDuSite{
		soumise: sertLaRecetteIllustree("Tarte", serveur.URL+"/recettes/tarte", "/photo.png", "200 g de farine"),
	})

	lot := lotDe(t, app, titulaire, soumise)
	traite(t, o, lot)

	recette := laRecetteDeLaLigne(t, app, lignesDuLot(t, app, lot)[0])
	if stocke := fichierStocke(t, app, recette); !bytes.Equal(stocke, octets) {
		t.Errorf("%d octets stockés, les %d octets servis attendus — l'adresse relative "+
			"n'a pas été résolue contre l'URL finale", len(stocke), len(octets))
	}
}

// TestUneImageQuiNAboutitPasNeCoutePasLaRecette : une image en 404 ne doit
// coûter ni la recette, ni une ligne au compteur des échecs. C'est ce que
// poseLImage tient déjà à l'unitaire, et la fournée n'a pas de raison d'être
// plus sévère — l'utilisateur a le formulaire d'édition pour l'illustrer.
func TestUneImageQuiNAboutitPasNeCoutePasLaRecette(t *testing.T) {
	cas := map[string]struct {
		// sert répond à la place du site d'images. Nil : la page ne publie
		// aucune adresse, et rien n'a à partir.
		sert http.HandlerFunc
	}{
		"image introuvable": {sert: func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }},
		"image illisible":   {sert: sertLesOctets([]byte("<html>ceci n'est pas une image</html>"), "image/png")},
		"page sans image":   {sert: nil},
	}

	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			app, titulaire, horloge, o := atelierDeLOuvrier(t)
			const adresse = "https://a.example/tarte"

			var image string
			if c.sert != nil {
				serveur := serveurDImages(t, c.sert)
				avecTelechargement(t, autoriseLesServeurs(serveur))
				image = serveur.URL + "/photo.png"
			}
			avecSite(t, horloge, map[string]reponseDuSite{
				adresse: sertLaRecetteIllustree("Tarte", adresse, image, "200 g de farine"),
			})

			lot := lotDe(t, app, titulaire, adresse)
			traite(t, o, lot)

			exigeRecetteSansImageEtLigneImportee(t, app, lignesDuLot(t, app, lot))
		})
	}
}

// TestUneImageDeFourneeVersUneAdresseNonRoutableNEstPasTelechargee : DOD.md §3.
// L'adresse ne vient pas de l'utilisateur mais d'une page tierce, et personne
// n'est devant l'écran pour la relire. Elle passe donc par la politique
// d'adresses de recuperation, après résolution DNS — un nom d'apparence
// publique qui pointe vers une IP privée est refusé comme l'IP elle-même.
func TestUneImageDeFourneeVersUneAdresseNonRoutableNEstPasTelechargee(t *testing.T) {
	cas := map[string]struct {
		image string
		// table est la résolution injectée, pour les seuls cas qui passent par
		// un nom d'hôte.
		table map[string]string
	}{
		"boucle locale":         {image: "http://127.0.0.1:8090/photo.png"},
		"métadonnées de l'hôte": {image: "http://169.254.169.254/latest/meta-data"},
		"réseau privé":          {image: "http://10.0.0.1/photo.png"},
		"nom public, adresse privée": {
			image: "http://images.exemple.fr/photo.png",
			table: map[string]string{"images.exemple.fr": "10.0.0.1"},
		},
		"schéma non suivi": {image: "file:///etc/passwd"},
	}

	for nom, c := range cas {
		t.Run(nom, func(t *testing.T) {
			app, titulaire, horloge, o := atelierDeLOuvrier(t)
			const adresse = "https://a.example/tarte"

			var choix []recuperation.Option
			if c.table != nil {
				choix = append(choix, resolutionDeTest(c.table))
			}
			avecTelechargement(t, choix...)
			avecSite(t, horloge, map[string]reponseDuSite{
				adresse: sertLaRecetteIllustree("Tarte", adresse, c.image, "200 g de farine"),
			})

			lot := lotDe(t, app, titulaire, adresse)
			traite(t, o, lot)

			exigeRecetteSansImageEtLigneImportee(t, app, lignesDuLot(t, app, lot))
		})
	}
}

// TestLImageDeLaFourneePasseParLaCadenceDeLaFournee : la politesse ne suit pas
// l'image toute seule. imageDistante ne passe que optionsDuTelechargement, vide
// en production ; la fournée, elle, tient sa cadence et son robots.txt retenu à
// la main. Sans les lui porter, l'image partirait hors du tour de rôle et son
// CDN paierait un robots.txt par page.
//
// Le compte se fait au transport, comme pour les pages : c'est là que la
// promesse porte.
func TestLImageDeLaFourneePasseParLaCadenceDeLaFournee(t *testing.T) {
	app, titulaire, horloge, o := atelierDeLOuvrier(t)
	const robots = "User-agent: *\nDisallow: /prive\n"
	const image = "https://images.example/photo.png"
	urls := []string{"https://a.example/1", "https://a.example/2"}

	corps := map[string]string{
		"https://a.example/robots.txt":      robots,
		"https://images.example/robots.txt": robots,
		image:                               string(pngDeTest(t)),
	}
	for i, adresse := range urls {
		corps[adresse] = string(htmlDeRecetteIllustree(
			"Recette "+string(rune('A'+i)), image, []string{"200 g de farine"}))
	}
	reseau := avecReseau(t, horloge, corps)
	avecTelechargement(t, recuperation.AvecTransport(reseau))

	traite(t, o, lotDe(t, app, titulaire, urls...))

	// Un robots.txt pour l'hôte des images, puis une image par page : il est
	// retenu pour la durée de la fournée, comme celui des pages.
	versLesImages := reseau.appelsVers("images.example")
	if len(versLesImages) != len(urls)+1 {
		t.Fatalf("%d requêtes vers l'hôte des images, attendu %d — un robots.txt et %d images :\n%v",
			len(versLesImages), len(urls)+1, len(urls), versLesImages)
	}
	if !strings.HasSuffix(versLesImages[0].url, "/robots.txt") {
		t.Errorf("première requête vers %q, attendu le robots.txt", versLesImages[0].url)
	}
	for i, ecart := range ecartsEntre(versLesImages) {
		if ecart < delaiEntreRequetes {
			t.Errorf("requêtes %q et %q espacées de %v, attendu au moins %v",
				versLesImages[i].url, versLesImages[i+1].url, ecart, delaiEntreRequetes)
		}
	}
}
