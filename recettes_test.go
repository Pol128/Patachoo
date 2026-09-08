package main

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"
)

// --- Montage ---------------------------------------------------------------

// carnetDeTest monte le serveur complet, crée le compte de test et ouvre sa
// session : la liste est derrière la session, donc tout test qui veut la lire
// commence par là.
func carnetDeTest(t *testing.T) (core.App, http.Handler, *http.Cookie) {
	t.Helper()

	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	return app, mux, cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
}

// demande joue une requête GET portant le cookie de session et les en-têtes
// donnés — c'est par là que passent tous les tests de cette page.
func demande(mux http.Handler, cible string, cookie *http.Cookie, entetes map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, cible, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for nom, valeur := range entetes {
		req.Header.Set(nom, valeur)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// listeDe rend le corps de la page de liste pour la cible donnée.
func listeDe(t *testing.T, mux http.Handler, cookie *http.Cookie, cible string) string {
	t.Helper()

	rec := demande(mux, cible, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour %s, attendu %d", rec.Code, cible, http.StatusOK)
	}
	return rec.Body.String()
}

// rechercheDe rend le corps de la page pour un terme, quel qu'il soit : c'est
// url.Values qui l'encode, pour qu'un « % » ou un « < » atteigne la route tel
// que l'utilisateur l'a tapé.
func rechercheDe(t *testing.T, mux http.Handler, cookie *http.Cookie, terme string) string {
	t.Helper()

	return listeDe(t, mux, cookie, "/recettes?"+url.Values{"q": {terme}}.Encode())
}

// --- Fixtures --------------------------------------------------------------

// recetteVoulue décrit la recette qu'un test veut en base. Les champs vides
// sont des absences réelles : une recette sans image, sans type de plat et
// sans tag est un cas normal du carnet.
type recetteVoulue struct {
	titre       string
	typeDePlat  string
	tags        []string
	ingredients []string
	avecImage   bool
	cree        time.Time
}

// creeRecette écrit la recette, ses lignes d'ingrédients et ses tags.
//
// Directement par app.Save, sans passer par l'API : cette tâche ne dépend ni
// de l'import ni de la route de création, qui n'existe pas encore.
func creeRecette(t *testing.T, app core.App, voulue recetteVoulue) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}

	enregistrement := core.NewRecord(collection)
	enregistrement.Set("title", voulue.titre)

	if voulue.typeDePlat != "" {
		enregistrement.Set("meal_type", typeDePlat(t, app, voulue.typeDePlat).Id)
	}
	if len(voulue.tags) > 0 {
		enregistrement.Set("tags", identifiants(creeTags(t, app, voulue.tags)))
	}
	if voulue.avecImage {
		enregistrement.Set("image", imageDeTest(t))
	}
	if !voulue.cree.IsZero() {
		// SetRaw, et non Set : le champ created est un autodate, et PocketBase
		// ne respecte une date posée à la main que par là (core/field_autodate.go).
		// Sans ça, trois recettes créées dans la même milliseconde sortiraient
		// dans un ordre que rien ne fixe.
		date, err := types.ParseDateTime(voulue.cree)
		if err != nil {
			t.Fatalf("date de création %v : %v", voulue.cree, err)
		}
		enregistrement.SetRaw("created", date)
	}

	if err := app.Save(enregistrement); err != nil {
		t.Fatalf("enregistrement de la recette %q : %v", voulue.titre, err)
	}

	creeIngredients(t, app, enregistrement, voulue.ingredients)
	return enregistrement
}

// typeDePlat rend un type de plat semé par la migration, ou fait échouer le
// test : les huit valeurs sont posées à l'installation, un test n'en invente
// pas une neuvième.
func typeDePlat(t *testing.T, app core.App, nom string) *core.Record {
	t.Helper()

	enregistrement, err := app.FindFirstRecordByData("meal_types", "name", nom)
	if err != nil {
		t.Fatalf("type de plat %q : %v", nom, err)
	}
	return enregistrement
}

func creeTags(t *testing.T, app core.App, noms []string) []*core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}

	tags := make([]*core.Record, 0, len(noms))
	for _, nom := range noms {
		tag := core.NewRecord(collection)
		tag.Set("name", nom)
		if err := app.Save(tag); err != nil {
			t.Fatalf("création du tag %q : %v", nom, err)
		}
		tags = append(tags, tag)
	}
	return tags
}

func creeIngredients(t *testing.T, app core.App, recette *core.Record, lignes []string) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("ingredients")
	if err != nil {
		t.Fatalf("collection ingredients : %v", err)
	}

	for position, ligne := range lignes {
		enregistrement := core.NewRecord(collection)
		enregistrement.Set("recipe", recette.Id)
		enregistrement.Set("position", position)
		enregistrement.Set("raw", ligne)
		if err := app.Save(enregistrement); err != nil {
			t.Fatalf("ingrédient %q : %v", ligne, err)
		}
	}
}

// imageDeTest rend une image minuscule mais réelle : le champ image valide le
// type MIME par le contenu, une suite d'octets quelconque serait refusée.
func imageDeTest(t *testing.T) *filesystem.File {
	t.Helper()

	var tampon bytes.Buffer
	if err := png.Encode(&tampon, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("encodage de l'image de test : %v", err)
	}

	fichier, err := filesystem.NewFileFromBytes(tampon.Bytes(), "vignette.png")
	if err != nil {
		t.Fatalf("fichier de test : %v", err)
	}
	return fichier
}

// instant rend une date fixe décalée de n minutes : l'ordre des recettes d'un
// test ne doit rien devoir à la vitesse de la machine.
func instant(n int) time.Time {
	return time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
}

// contient dit si le corps rendu porte chacun des fragments attendus.
func exigeContient(t *testing.T, corps string, attendus ...string) {
	t.Helper()

	for _, attendu := range attendus {
		if !strings.Contains(corps, attendu) {
			t.Errorf("la page ne contient pas %q :\n%s", attendu, corps)
		}
	}
}

func exigeSansAucun(t *testing.T, corps string, interdits ...string) {
	t.Helper()

	for _, interdit := range interdits {
		if strings.Contains(corps, interdit) {
			t.Errorf("la page contient %q, qu'elle ne devrait pas :\n%s", interdit, corps)
		}
	}
}

// --- URL canonique ---------------------------------------------------------

func TestLAccueilRedirigeVersLaListe(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	rec := demande(mux, "/", cookie, nil)

	if rec.Code != http.StatusFound {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusFound)
	}
	if lieu := rec.Header().Get("Location"); lieu != "/recettes" {
		t.Errorf("Location %q, attendu %q", lieu, "/recettes")
	}
}

// --- La grille -------------------------------------------------------------

func TestLaListePorteLeTitreDeChaqueRecette(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps, "Tarte aux pommes", "Soupe de potiron")
}

func TestLaListeVaDeLaPlusRecenteALaPlusAncienne(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "La plus ancienne", cree: instant(0)})
	creeRecette(t, app, recetteVoulue{titre: "Celle du milieu", cree: instant(1)})
	creeRecette(t, app, recetteVoulue{titre: "La plus recente", cree: instant(2)})

	corps := listeDe(t, mux, cookie, "/recettes")

	ordre := []string{"La plus recente", "Celle du milieu", "La plus ancienne"}
	precedent := -1
	for _, titre := range ordre {
		position := strings.Index(corps, titre)
		if position < 0 {
			t.Fatalf("titre %q absent de la page :\n%s", titre, corps)
		}
		if position < precedent {
			t.Errorf("ordre rompu : %q apparaît avant ce qui devrait le précéder\n%s", titre, corps)
		}
		precedent = position
	}
}

func TestLaVignettePorteImageTypeDePlatTagsEtLien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := creeRecette(t, app, recetteVoulue{
		titre:      "Tarte au citron",
		typeDePlat: "Dessert",
		tags:       []string{"gouter"},
		avecImage:  true,
	})

	corps := listeDe(t, mux, cookie, "/recettes")

	fichier := recette.GetString("image")
	if fichier == "" {
		t.Fatal("l'image n'a pas été stockée : la vignette n'a rien à montrer")
	}
	exigeContient(t, corps,
		"Tarte au citron",
		"/api/files/recipes/"+recette.Id+"/"+fichier+"?thumb=300x200",
		"Dessert",
		"gouter",
		`href="/recettes/`+recette.Id+`"`,
	)
}

// Sans image, sans type de plat et sans tag : rien à la place, ni cadre vide
// ni libellé orphelin.
func TestUneRecetteNueRendUneVignetteLisible(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Pain perdu"})

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps, "Pain perdu")
	exigeSansAucun(t, corps, "<img", "/api/files/")
}

// --- La recherche ----------------------------------------------------------

func TestLaRechercheTrouveParLeTitre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := rechercheDe(t, mux, cookie, "pomme")

	exigeContient(t, corps, "Tarte aux pommes")
	exigeSansAucun(t, corps, "Soupe de potiron")
}

// Trois lignes d'ingrédient, une seule qui corresponde : c'est ce qui
// distingue ?~ de ~. Un ~ écrit par erreur ne ramène rien et rougit ici.
func TestLaRechercheTrouveParUneSeuleLigneDIngredient(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{
		titre:       "Gateau du dimanche",
		ingredients: []string{"200 g de farine", "3 oeufs", "1 gousse de vanille"},
	})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := rechercheDe(t, mux, cookie, "vanille")

	exigeContient(t, corps, "Gateau du dimanche")
	exigeSansAucun(t, corps, "Soupe de potiron")
}

// Deux tags, un seul qui corresponde : même piège que sur les ingrédients.
func TestLaRechercheTrouveParUnSeulNomDeTag(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Omelette", tags: []string{"rapide", "vegetarien"}})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := rechercheDe(t, mux, cookie, "rapide")

	exigeContient(t, corps, "Omelette")
	exigeSansAucun(t, corps, "Soupe de potiron")
}

// La jointure sur les lignes d'ingrédient multiplie les lignes : sans
// déduplication, la recette sortirait trois fois.
func TestUneRecetteNApparaitQuUneFois(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{
		titre:       "Gateau du dimanche",
		ingredients: []string{"100 g de sucre roux", "1 sachet de sucre vanille", "sucre glace"},
	})

	corps := rechercheDe(t, mux, cookie, "sucre")

	if compte := strings.Count(corps, "Gateau du dimanche"); compte != 1 {
		t.Errorf("la recette apparaît %d fois, attendu 1 :\n%s", compte, corps)
	}
}

func TestUneRechercheSansResultatLeDitEtNeRendPasLeCarnet(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	corps := rechercheDe(t, mux, cookie, "cassoulet")

	exigeSansAucun(t, corps, "Tarte aux pommes")
	if !strings.Contains(corps, "Aucune recette") {
		t.Errorf("pas de message d'absence de résultat :\n%s", corps)
	}
}

// Le % et le _ sont les jokers de LIKE. Une valeur liée qui en porte un est
// laissée telle quelle par PocketBase : sans échappement de notre côté, une
// recherche sur « % » ramènerait tout le carnet.
func TestLesJokersSontCherchesLitteralement(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})
	creeRecette(t, app, recetteVoulue{titre: "A"})
	creeRecette(t, app, recetteVoulue{titre: "Reduction 50% de sel"})

	surPourcent := rechercheDe(t, mux, cookie, "%")
	exigeContient(t, surPourcent, "Reduction 50% de sel")
	exigeSansAucun(t, surPourcent, "Tarte aux pommes")

	surSoulignement := rechercheDe(t, mux, cookie, "_")
	exigeSansAucun(t, surSoulignement, "Tarte aux pommes", ">A<")
}

// La limite assumée : LIKE ne replie pas les accents. C'est ce test que
// PATA-31 inversera.
func TestLaRechercheNeReplitPasLesAccents(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Creme brulee"})
	creeRecette(t, app, recetteVoulue{titre: "Crème brûlée"})

	surSansAccent := rechercheDe(t, mux, cookie, "creme")
	exigeContient(t, surSansAccent, "Creme brulee")
	exigeSansAucun(t, surSansAccent, "Crème brûlée")

	surAccentue := rechercheDe(t, mux, cookie, "Crème")
	exigeContient(t, surAccentue, "Crème brûlée")
}

// --- La pagination ---------------------------------------------------------

// vingtCinqRecettes remplit le carnet d'une page pleine plus une.
func vingtCinqRecettes(t *testing.T, app core.App) {
	t.Helper()

	for i := 0; i < 25; i++ {
		creeRecette(t, app, recetteVoulue{titre: numero(i), cree: instant(i)})
	}
}

// numero rend un titre reconnaissable et non préfixe d'un autre : « R-1 » se
// retrouverait dans « R-10 », et le compte des vignettes serait faux.
func numero(i int) string {
	return "Recette numero " + string(rune('A'+i/10)) + string(rune('0'+i%10)) + " du carnet"
}

func TestLaPremierePageAfficheVingtQuatreVignettesEtProposeLaSuivante(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)

	corps := listeDe(t, mux, cookie, "/recettes")

	if compte := strings.Count(corps, "Recette numero "); compte != 24 {
		t.Errorf("%d vignettes sur la première page, attendu 24", compte)
	}
	// La plus récente d'abord : la 25e créée est en tête, la première créée
	// est la seule à déborder sur la page 2.
	exigeSansAucun(t, corps, numero(0))
	exigeContient(t, corps, `rel="next" href="/recettes?page=2"`)
}

func TestLaSecondePageAfficheLeResteEtNeProposePasDeSuivante(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?page=2")

	if compte := strings.Count(corps, "Recette numero "); compte != 1 {
		t.Errorf("%d vignettes sur la seconde page, attendu 1", compte)
	}
	exigeContient(t, corps, numero(0), `rel="prev" href="/recettes"`)
	exigeSansAucun(t, corps, `rel="next"`)
}

func TestUnePageMalmeneeRendLaPremiere(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)
	premiere := listeDe(t, mux, cookie, "/recettes")
	exigeContient(t, premiere, numero(24))

	for _, cible := range []string{"/recettes?page=0", "/recettes?page=-3", "/recettes?page=abc", "/recettes?page="} {
		if corps := listeDe(t, mux, cookie, cible); corps != premiere {
			t.Errorf("%s ne rend pas la première page :\n%s", cible, corps)
		}
	}
}

// Une page au-delà du dernier rang est une liste vide, pas une 500 — et une
// page démesurée ne doit pas non plus faire déborder le calcul du décalage.
func TestUnePageAuDelaDuDernierRangEstVideSansErreur(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)

	for _, cible := range []string{"/recettes?page=3", "/recettes?page=999999999999999999"} {
		corps := listeDe(t, mux, cookie, cible)
		if strings.Contains(corps, "Recette numero ") {
			t.Errorf("%s rend des vignettes alors qu'il n'y en a plus :\n%s", cible, corps)
		}
		exigeContient(t, corps, "Aucune recette")
	}
}

func TestLaRechercheEtLaPaginationSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)
	creeRecette(t, app, recetteVoulue{titre: "Cassoulet", cree: instant(100)})

	corps := listeDe(t, mux, cookie, "/recettes?q=numero")

	if compte := strings.Count(corps, "Recette numero "); compte != 24 {
		t.Errorf("%d vignettes, attendu 24", compte)
	}
	exigeSansAucun(t, corps, "Cassoulet")
	exigeContient(t, corps, `rel="next" href="/recettes?page=2&amp;q=numero"`)

	seconde := listeDe(t, mux, cookie, "/recettes?q=numero&page=2")
	exigeContient(t, seconde, numero(0), `rel="prev" href="/recettes?q=numero"`)
}

// --- Les deux absences -----------------------------------------------------

func TestLeCarnetVideInviteACreerUneRecette(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps, `href="/recettes/nouvelle"`)
	if !strings.Contains(corps, "carnet est vide") {
		t.Errorf("pas d'invitation à créer une recette :\n%s", corps)
	}
}

// Confondre les deux afficherait « votre carnet est vide » à quelqu'un qui a
// simplement mal orthographié un mot.
func TestLInvitationNApparaitPasQuandLaRechercheEchoue(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	corps := rechercheDe(t, mux, cookie, "cassoulet")

	exigeContient(t, corps, "Aucune recette")
	if strings.Contains(corps, "carnet est vide") {
		t.Errorf("l'invitation du carnet vide s'affiche sur une recherche sans résultat :\n%s", corps)
	}
}

// --- HTMX ------------------------------------------------------------------

func TestLeChampDeRechercheEstBrancheSurHTMX(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps,
		`hx-get="/recettes"`,
		`hx-trigger="keyup changed delay:300ms"`,
		// Sans JavaScript, le formulaire reste soumissible : même route, même
		// rendu, en page complète.
		`method="get"`,
		`action="/recettes"`,
		`name="q"`,
	)
}

func TestHTMXNeRecoitQueLeFragmentDeResultats(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	rec := demande(mux, "/recettes", cookie, map[string]string{"HX-Request": "true"})
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeSansAucun(t, corps, "<html", "<body")
	exigeContient(t, corps, "Tarte aux pommes")
}

func TestSansHTMXLaPageEstComplete(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps, "<!doctype html>", "<html", "<body", "Tarte aux pommes")
}

// --- Échappement (DOD.md §3) ----------------------------------------------

// Quatre emplacements, quatre chemins de rendu : le titre, le tag, la valeur
// du champ de recherche et le message d'absence.

func TestLaGrilleEchappeLeTitre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: `<script>alert(1)</script>`})

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeSansAucun(t, corps, "<script>alert(1)</script>")
	exigeContient(t, corps, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestLaGrilleEchappeLesTags(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes", tags: []string{`"><script>`}})

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeSansAucun(t, corps, `"><script>`)
	exigeContient(t, corps, "&lt;script&gt;")
}

func TestLeChampDeRechercheEchappeLeTerme(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	corps := rechercheDe(t, mux, cookie, `<script>alert(1)</script>`)

	valeur := entreBalises(corps, `name="q" value="`, `"`)
	if valeur == "" {
		t.Fatalf("valeur du champ de recherche introuvable :\n%s", corps)
	}
	if strings.Contains(valeur, "<script>") {
		t.Errorf("terme non échappé dans le champ : %q", valeur)
	}
	exigeSansAucun(t, corps, "<script>alert(1)</script>")
}

func TestLeMessageDAbsenceEchappeLeTerme(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	corps := rechercheDe(t, mux, cookie, `<script>alert(1)</script>`)

	message := entreBalises(corps, `<p class="absence">`, "</p>")
	if message == "" {
		t.Fatalf("message d'absence introuvable :\n%s", corps)
	}
	if !strings.Contains(message, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("terme absent ou non échappé du message : %q", message)
	}
}

// --- La session ------------------------------------------------------------

func TestSansSessionLeCarnetNEstPasRendu(t *testing.T) {
	app, mux := serveurDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	rec := demande(mux, "/recettes", nil, nil)

	if rec.Code != http.StatusFound {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusFound)
	}
	if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
		t.Errorf("Location %q, attendu %q", lieu, "/connexion")
	}
	exigeSansAucun(t, rec.Body.String(), "Tarte aux pommes")
}

func TestAvecSessionLeCarnetEstRendu(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps, "Tarte aux pommes")
}
