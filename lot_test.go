package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// --- Montage ---------------------------------------------------------------

// atelierDeLot monte le carnet, ouvre une session, et pose le piège réseau.
//
// Le piège est ici, et non dans un test dédié : le critère porte sur *tous*
// les cas de ces deux routes, et un test qui ne l'armerait qu'une fois ne
// dirait rien des autres. Armé pour chaque test, il ne peut plus être oublié.
func atelierDeLot(t *testing.T) (core.App, http.Handler, *http.Cookie) {
	t.Helper()

	piegeLeReseau(t)
	return carnetDeTest(t)
}

// piegeLeReseau fait échouer le test à la première requête sortante.
//
// http.DefaultTransport est le seul point de passage qu'un appel sortant écrit
// sans y penser emprunterait : http.Get, http.DefaultClient, et tout client
// bâti sans transport explicite le lisent à chaque requête.
func piegeLeReseau(t *testing.T) {
	t.Helper()

	initial := http.DefaultTransport
	http.DefaultTransport = transportPiege{t}
	t.Cleanup(func() { http.DefaultTransport = initial })
}

// transportPiege est le transport qui n'en est pas un.
//
// Nommé pour ce qu'il est : reseauPiege est déjà, dans import_test.go, la
// fonction qui garde la couture recuperePage. Deux pièges à deux étages,
// et un seul paquet de test pour les deux.
type transportPiege struct{ t *testing.T }

func (p transportPiege) RoundTrip(r *http.Request) (*http.Response, error) {
	p.t.Errorf("requête sortante vers %s alors qu'aucune ne devait partir", r.URL)
	return nil, errors.New("piège")
}

// soumetLeLot poste la saisie sur la route de lancement.
func soumetLeLot(mux http.Handler, cookie *http.Cookie, saisie string) *httptest.ResponseRecorder {
	champs := url.Values{"urls": {saisie}}
	req := httptest.NewRequest(http.MethodPost, cheminDuLot, strings.NewReader(champs.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// lotAccepte poste la saisie et exige que le lot ait été créé.
func lotAccepte(t *testing.T, mux http.Handler, cookie *http.Cookie, saisie string) string {
	t.Helper()

	rec := soumetLeLot(mux, cookie, saisie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d au lancement du lot, attendu %d", rec.Code, http.StatusOK)
	}
	return rec.Body.String()
}

// compte rend le nombre d'enregistrements d'une collection.
func compte(t *testing.T, app core.App, collection string) int {
	t.Helper()

	total, err := app.CountRecords(collection)
	if err != nil {
		t.Fatalf("comptage de %s : %v", collection, err)
	}
	return int(total)
}

// lignesDuLot rend les lignes d'un lot, dans l'ordre de saisie.
func lignesDuLot(t *testing.T, app core.App, lot *core.Record) []*core.Record {
	t.Helper()

	lignes, err := app.FindAllRecords("import_urls", dbx.HashExp{"batch": lot.Id})
	if err != nil {
		t.Fatalf("lecture des lignes du lot : %v", err)
	}
	trieParPosition(lignes)
	return lignes
}

// leSeulLot rend l'unique enregistrement imports, ou fait échouer le test.
func leSeulLot(t *testing.T, app core.App) *core.Record {
	t.Helper()

	lots, err := app.FindAllRecords("imports")
	if err != nil {
		t.Fatalf("lecture des lots : %v", err)
	}
	if len(lots) != 1 {
		t.Fatalf("%d enregistrements imports, attendu 1", len(lots))
	}
	return lots[0]
}

// leCompteDeLaSession relit le compte que carnetDeTest a créé : le recréer
// buterait sur l'unicité du courriel.
func leCompteDeLaSession(t *testing.T, app core.App) *core.Record {
	t.Helper()

	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("compte de la session : %v", err)
	}
	return compte
}

// urlsDeTest rend n URLs distinctes et valides.
func urlsDeTest(n int) []string {
	adresses := make([]string, 0, n)
	for i := 0; i < n; i++ {
		adresses = append(adresses, fmt.Sprintf("https://exemple.fr/recette-%d", i))
	}
	return adresses
}

// --- La session ------------------------------------------------------------

// Sans session, la page ne rend rien d'exploitable : elle renvoie à la
// connexion. Le piège réseau, armé par atelierDeLot, dit au passage qu'aucune
// requête n'est partie.
func TestLaPageDuLotExigeUneSession(t *testing.T) {
	app, mux, _ := atelierDeLot(t)

	rec := avecCookie(mux, http.MethodGet, cheminDuLot, nil)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d pour un visiteur, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if destination := rec.Header().Get("Location"); destination != "/connexion" {
		t.Errorf("redirection vers %q, attendu %q", destination, "/connexion")
	}
	for _, phrase := range disclaimerDuLot {
		if strings.Contains(rec.Body.String(), phrase) {
			t.Errorf("la page a été rendue à un visiteur :\n%s", rec.Body.String())
		}
	}
	if n := compte(t, app, "imports"); n != 0 {
		t.Errorf("%d enregistrements imports après un GET de visiteur, attendu 0", n)
	}
}

// Sans session, la route de lancement n'écrit rien : ni lot, ni ligne, ni tag.
func TestLeLancementDuLotExigeUneSession(t *testing.T) {
	app, mux, _ := atelierDeLot(t)

	tagsAvant := compte(t, app, "tags")
	rec := soumetLeLot(mux, nil, strings.Join(urlsDeTest(3), "\n"))

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d pour un visiteur, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if destination := rec.Header().Get("Location"); destination != "/connexion" {
		t.Errorf("redirection vers %q, attendu %q", destination, "/connexion")
	}
	if n := compte(t, app, "imports"); n != 0 {
		t.Errorf("%d enregistrements imports pour un visiteur, attendu 0", n)
	}
	if n := compte(t, app, "import_urls"); n != 0 {
		t.Errorf("%d lignes import_urls pour un visiteur, attendu 0", n)
	}
	if n := compte(t, app, "tags"); n != tagsAvant {
		t.Errorf("%d tags après le refus, attendu %d", n, tagsAvant)
	}
}

// --- Le lot ordinaire ------------------------------------------------------

// N URLs valides donnent un lot et N lignes à faire, attribuées au compte de
// la session.
func TestUnLotCreeUnEnregistrementEtUneLigneParURL(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	adresses := urlsDeTest(3)
	corps := lotAccepte(t, mux, cookie, strings.Join(adresses, "\n"))

	lot := leSeulLot(t, app)
	if statut := lot.GetString("status"); statut != "en_cours" {
		t.Errorf("statut du lot %q, attendu %q", statut, "en_cours")
	}

	compteDeLaSession := leCompteDeLaSession(t, app)
	if auteur := lot.GetString("created_by"); auteur != compteDeLaSession.Id {
		t.Errorf("created_by %q, attendu %q", auteur, compteDeLaSession.Id)
	}

	lignes := lignesDuLot(t, app, lot)
	if len(lignes) != len(adresses) {
		t.Fatalf("%d lignes import_urls, attendu %d", len(lignes), len(adresses))
	}
	for i, ligne := range lignes {
		if adresse := ligne.GetString("url"); adresse != adresses[i] {
			t.Errorf("ligne %d : url %q, attendu %q", i, adresse, adresses[i])
		}
		if statut := ligne.GetString("status"); statut != "a_faire" {
			t.Errorf("ligne %d : statut %q, attendu %q", i, statut, "a_faire")
		}
		if rang := ligne.GetInt("position"); rang != i+1 {
			t.Errorf("ligne %d : position %d, attendu %d", i, rang, i+1)
		}
	}

	if !strings.Contains(corps, "<strong>3</strong>") {
		t.Errorf("la réponse ne donne pas le nombre d'URLs retenues :\n%s", corps)
	}
}

// La même URL répétée ne part qu'une fois : l'index unique (batch, url) est le
// filet, pas le filtre.
func TestLesDoublonsDeLaSaisieSontFusionnes(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	adresse := "https://exemple.fr/tarte-aux-pommes"
	lotAccepte(t, mux, cookie, strings.Join([]string{adresse, adresse, adresse}, "\n"))

	if n := compte(t, app, "import_urls"); n != 1 {
		t.Errorf("%d lignes import_urls pour trois fois la même URL, attendu 1", n)
	}
}

// --- La validation ---------------------------------------------------------

// Les lignes écartées le sont avec leur motif, et n'empêchent pas les autres
// de partir.
func TestLesLignesEcarteesNEmpechentPasLesAutres(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	saisie := strings.Join([]string{
		"https://exemple.fr/premiere",
		"",
		"   ",
		"/recettes/relative",
		"ftp://exemple.fr/fichier",
		"file:///etc/passwd",
		"http://exemple.fr/seconde",
	}, "\n")

	corps := lotAccepte(t, mux, cookie, saisie)

	if n := compte(t, app, "import_urls"); n != 2 {
		t.Errorf("%d lignes import_urls, attendu 2", n)
	}

	// Les lignes vides ne sont pas des erreurs : elles ne sont pas listées.
	for _, ecartee := range []string{"/recettes/relative", "ftp://exemple.fr/fichier", "file:///etc/passwd"} {
		if !strings.Contains(corps, html.EscapeString(ecartee)) {
			t.Errorf("la réponse ne signale pas la ligne écartée %q :\n%s", ecartee, corps)
		}
	}
	// Les deux lignes vides ne sont comptées nulle part : trois écartées, et
	// pas cinq.
	if n := strings.Count(corps, `class="ecartee"`); n != 3 {
		t.Errorf("%d lignes écartées listées, attendu 3 :\n%s", n, corps)
	}
}

// Chaque ligne écartée ressort avec le motif qui la vise, et non un refus
// générique : quatre causes, quatre remèdes, et « adresse relative » ne se
// corrige pas comme « schéma ftp ».
func TestChaqueLigneEcarteeDonneSonMotif(t *testing.T) {
	_, mux, cookie := atelierDeLot(t)

	cas := []struct {
		ligne string
		motif string
	}{
		{"/recettes/relative", "adresse relative : une URL complète est attendue"},
		// Le schéma est recopié dans le motif : ftp et file n'ont pas le même
		// remède, et un message unique les confondrait.
		{"ftp://exemple.fr/fichier", "schéma « ftp » : seuls http et https sont acceptés"},
		{"file:///etc/passwd", "schéma « file » : seuls http et https sont acceptés"},
		// url.Parse bute sur l'échappement %zz : la ligne n'est même pas une
		// adresse.
		{"https://exemple.fr/%zz", "adresse illisible"},
		// L'analyse passe, mais il ne reste aucun hôte à joindre.
		{"http://", "adresse sans nom de domaine"},
	}

	// Une URL retenue accompagne les fautives : sans elle, le lot serait
	// refusé en entier et la liste des motifs ne serait jamais rendue.
	saisie := []string{"https://exemple.fr/retenue"}
	for _, c := range cas {
		saisie = append(saisie, c.ligne)
	}
	corps := lotAccepte(t, mux, cookie, strings.Join(saisie, "\n"))

	entrees := lignesEcarteesRendues(corps)
	if len(entrees) != len(cas) {
		t.Fatalf("%d lignes écartées rendues, attendu %d :\n%s", len(entrees), len(cas), corps)
	}
	for _, c := range cas {
		entree := entreeDeLaLigne(entrees, c.ligne)
		if entree == "" {
			t.Errorf("la ligne %q n'est pas listée :\n%s", c.ligne, corps)
			continue
		}
		if !strings.Contains(entree, html.EscapeString(c.motif)) {
			t.Errorf("la ligne %q est rendue %q, sans son motif %q", c.ligne, entree, c.motif)
		}
	}
}

// lignesEcarteesRendues extrait le contenu de chaque entrée de la liste des
// lignes écartées, dans l'ordre du rendu.
func lignesEcarteesRendues(corps string) []string {
	var entrees []string
	for _, morceau := range strings.Split(corps, `<li class="ecartee">`)[1:] {
		if fin := strings.Index(morceau, "</li>"); fin >= 0 {
			entrees = append(entrees, morceau[:fin])
		}
	}
	return entrees
}

// entreeDeLaLigne rend l'entrée qui porte cette ligne, ou "" si aucune ne la
// porte.
func entreeDeLaLigne(entrees []string, ligne string) string {
	for _, entree := range entrees {
		if strings.Contains(entree, html.EscapeString(ligne)) {
			return entree
		}
	}
	return ""
}

// Une ligne écartée ressort échappée : c'est du texte étranger rendu dans une
// page (DOD.md §3).
func TestUneLigneEcarteeEstEchappee(t *testing.T) {
	_, mux, cookie := atelierDeLot(t)

	corps := lotAccepte(t, mux, cookie, "<script>alert(1)</script>\nhttps://exemple.fr/recette")

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("la ligne écartée ressort telle quelle :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("la ligne écartée n'apparaît pas échappée :\n%s", corps)
	}
}

// Au-delà du plafond, le lot est refusé en entier, avant toute écriture, et le
// message donne le compte reçu.
func TestLePlafondRefuseLeLotEnEntier(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	tagsAvant := compte(t, app, "tags")
	rec := soumetLeLot(mux, cookie, strings.Join(urlsDeTest(plafondDuLot+1), "\n"))

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour un lot refusé, attendu %d", rec.Code, http.StatusOK)
	}
	if n := compte(t, app, "imports"); n != 0 {
		t.Errorf("%d enregistrements imports pour un lot refusé, attendu 0", n)
	}
	if n := compte(t, app, "import_urls"); n != 0 {
		t.Errorf("%d lignes import_urls pour un lot refusé, attendu 0", n)
	}
	if n := compte(t, app, "tags"); n != tagsAvant {
		t.Errorf("%d tags après un lot refusé, attendu %d", n, tagsAvant)
	}

	message := entreBalises(rec.Body.String(), `<p class="erreur" role="alert">`, "</p>")
	if !strings.Contains(message, fmt.Sprint(plafondDuLot+1)) {
		t.Errorf("le message %q ne donne pas le compte reçu (%d)", message, plafondDuLot+1)
	}
}

// Une saisie dont rien ne survit à la validation ne crée pas de lot vide : il
// n'y aurait rien à en faire, et le tag de la fournée resterait orphelin.
func TestUneSaisieSansAucuneURLRetenueNeCreeAucunLot(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	tagsAvant := compte(t, app, "tags")
	// Rien que des blancs : c'est le garde-fou du lot vide qui est en jeu, et
	// lui seul. Une ligne fautive y mêlerait la règle qui l'écarte.
	rec := soumetLeLot(mux, cookie, "\n   \n\t\n")

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if n := compte(t, app, "imports"); n != 0 {
		t.Errorf("%d enregistrements imports sans aucune URL retenue, attendu 0", n)
	}
	if n := compte(t, app, "tags"); n != tagsAvant {
		t.Errorf("%d tags sans aucune URL retenue, attendu %d", n, tagsAvant)
	}
}

// Un lot refusé rend la saisie dans le formulaire : une liste collée ne se
// retape pas parce qu'une ligne sur vingt était fautive.
func TestUnLotRefuseReproposeLaSaisie(t *testing.T) {
	_, mux, cookie := atelierDeLot(t)

	lignes := []string{"ftp://exemple.fr/fichier", "file:///etc/passwd"}
	rec := soumetLeLot(mux, cookie, strings.Join(lignes, "\n"))

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour un lot refusé, attendu %d", rec.Code, http.StatusOK)
	}

	champ := entreBalises(rec.Body.String(), `name="urls"`, "</textarea>")
	for _, ligne := range lignes {
		if !strings.Contains(champ, html.EscapeString(ligne)) {
			t.Errorf("la ligne %q n'est pas reproposée dans le champ de saisie %q", ligne, champ)
		}
	}
}

// --- Le disclaimer ---------------------------------------------------------

// Les trois phrases sont sur la page, et au-dessus du bouton qui lance le lot
// — pas dans une page d'aide que personne n'ouvre.
func TestLeDisclaimerEstAuDessusDuBouton(t *testing.T) {
	_, mux, cookie := atelierDeLot(t)

	rec := demande(mux, cheminDuLot, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour la page du lot, attendu %d", rec.Code, http.StatusOK)
	}
	corps := rec.Body.String()

	bouton := strings.Index(corps, `id="lancer-le-lot"`)
	if bouton < 0 {
		t.Fatalf("aucun bouton dans la page :\n%s", corps)
	}
	for _, phrase := range disclaimerDuLot {
		place := strings.Index(corps, html.EscapeString(phrase))
		if place < 0 {
			t.Errorf("la page ne porte pas la phrase %q :\n%s", phrase, corps)
			continue
		}
		if place > bouton {
			t.Errorf("la phrase %q est sous le bouton, attendu au-dessus", phrase)
		}
	}
}

// --- Le tag de la fournée --------------------------------------------------

// Un lot crée exactement un tag, et le lot pointe dessus.
func TestUnLotCreeExactementUnTag(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	tagsAvant := compte(t, app, "tags")
	corps := lotAccepte(t, mux, cookie, strings.Join(urlsDeTest(2), "\n"))

	if n := compte(t, app, "tags"); n != tagsAvant+1 {
		t.Fatalf("%d tags après le lot, attendu %d", n, tagsAvant+1)
	}

	lot := leSeulLot(t, app)
	tag, err := app.FindRecordById("tags", lot.GetString("tag"))
	if err != nil {
		t.Fatalf("imports.tag ne désigne aucun tag : %v", err)
	}
	if !strings.Contains(corps, html.EscapeString(tag.GetString("name"))) {
		t.Errorf("la réponse ne donne pas le nom du tag %q :\n%s", tag.GetString("name"), corps)
	}
}

// Deux lots de la même minute donnent deux tags distincts. L'horloge est
// injectée : sans elle, le cas ne se produirait qu'au hasard de la seconde où
// le test tourne.
func TestDeuxLotsDeLaMemeMinuteDonnentDeuxSlugsDistincts(t *testing.T) {
	app, _, _ := atelierDeLot(t)
	compteDeTest := leCompteDeLaSession(t, app)

	instant := time.Date(2026, 8, 21, 23, 44, 12, 0, time.UTC)

	_, premier, err := creeLeLot(app, compteDeTest, urlsDeTest(1), instant)
	if err != nil {
		t.Fatalf("premier lot : %v", err)
	}
	_, second, err := creeLeLot(app, compteDeTest, urlsDeTest(1), instant.Add(30*time.Second))
	if err != nil {
		t.Fatalf("second lot : %v", err)
	}

	if premier.GetString("slug") == second.GetString("slug") {
		t.Errorf("les deux lots partagent le slug %q", premier.GetString("slug"))
	}
	if premier.GetString("slug") != "import-du-21-08-2026-a-23h44" {
		t.Errorf("slug du premier lot %q, attendu %q", premier.GetString("slug"), "import-du-21-08-2026-a-23h44")
	}
}

// --- La transaction --------------------------------------------------------

// Un échec au milieu du lot ne laisse rien derrière lui.
func TestUnEchecEnCoursDeLotNeLaisseRienDerriereLui(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	ecrites := 0
	app.OnRecordCreate("import_urls").BindFunc(func(e *core.RecordEvent) error {
		ecrites++
		if ecrites == 2 {
			return errors.New("écriture refusée par le test")
		}
		return e.Next()
	})

	rec := soumetLeLot(mux, cookie, strings.Join(urlsDeTest(3), "\n"))

	if rec.Code == http.StatusOK {
		t.Errorf("statut %d malgré l'échec d'écriture, attendu une erreur", rec.Code)
	}
	if n := compte(t, app, "imports"); n != 0 {
		t.Errorf("%d enregistrements imports après un échec, attendu 0", n)
	}
	if n := compte(t, app, "import_urls"); n != 0 {
		t.Errorf("%d lignes import_urls après un échec, attendu 0", n)
	}
}

// La borne de recherche d'un nom de tag libre n'est pas un ornement : les tags
// sont créés par la saisie libre du formulaire de recette, donc un compte peut
// occuper à la main tous les noms de la minute. Sans borne, le lot suivant
// balaierait les discriminants sans jamais s'arrêter (DOD.md §3, « Limites »).
func TestLaRechercheDUnNomDeTagLibreEstBornee(t *testing.T) {
	app, _, _ := atelierDeLot(t)
	compteDeTest := leCompteDeLaSession(t, app)

	instant := time.Date(2026, 8, 21, 23, 44, 0, 0, time.UTC)
	occupeLesNomsDeLaMinute(t, app, "Import du 21/08/2026 à 23h44", fourneesMaxParMinute)

	if _, _, err := creeLeLot(app, compteDeTest, urlsDeTest(1), instant); err == nil {
		t.Fatalf("lot créé alors que les %d noms de la minute sont pris", fourneesMaxParMinute)
	}
	if n := compte(t, app, "imports"); n != 0 {
		t.Errorf("%d enregistrements imports après le refus, attendu 0", n)
	}
}

// occupeLesNomsDeLaMinute crée les tags que le lot chercherait, du nom nu au
// dernier discriminant.
func occupeLesNomsDeLaMinute(t *testing.T, app core.App, base string, jusqua int) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}

	for rang := 1; rang <= jusqua; rang++ {
		nom := base
		if rang > 1 {
			nom = fmt.Sprintf("%s (%d)", base, rang)
		}
		tag := core.NewRecord(collection)
		tag.Set("name", nom)
		if err := app.Save(tag); err != nil {
			t.Fatalf("création du tag %q : %v", nom, err)
		}
	}
}
