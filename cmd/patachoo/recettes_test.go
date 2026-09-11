package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
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
	titre string
	// typeDePlat nomme un type semé par la migration ; typeDePlatId désigne
	// celui que le test a posé lui-même. Les deux plutôt qu'un seul : la
	// plupart des tests lisent la collection installée, et ceux du filtre ont
	// besoin d'y ajouter un homonyme ou un libellé dangereux.
	typeDePlat   string
	typeDePlatId string
	tags         []string
	saisons      []string
	ingredients  []string
	avecImage    bool
	cree         time.Time
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

	if voulue.typeDePlatId != "" {
		enregistrement.Set("meal_type", voulue.typeDePlatId)
	} else if voulue.typeDePlat != "" {
		enregistrement.Set("meal_type", typeDePlat(t, app, voulue.typeDePlat).Id)
	}
	if len(voulue.tags) > 0 {
		enregistrement.Set("tags", identifiants(creeTags(t, app, voulue.tags)))
	}
	// Les saisons sont un select, pas une relation : les valeurs accentuées du
	// schéma s'écrivent directement. Une liste vide est le cas normal — c'est
	// celui des recettes importées.
	if len(voulue.saisons) > 0 {
		enregistrement.Set("seasons", voulue.saisons)
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

// Le % et le _ sont les jokers de LIKE, et ce test dit qu'ils n'en sont pas :
// une recherche sur « % » ne ramène pas le carnet entier.
//
// Depuis la bascule vers FTS5 (PATA-31), ils ne ramènent plus rien du tout —
// la ponctuation n'est pas indexée, donc aucun terme réduit à un signe ne
// correspond à quoi que ce soit. L'assertion sur « Reduction 50% de sel »,
// que le LIKE échappé satisfaisait, tombe donc avec la bascule ; ce que le
// test protège, lui, ne bouge pas.
func TestLesJokersSontCherchesLitteralement(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})
	creeRecette(t, app, recetteVoulue{titre: "A"})
	creeRecette(t, app, recetteVoulue{titre: "Reduction 50% de sel"})

	surPourcent := rechercheDe(t, mux, cookie, "%")
	exigeSansAucun(t, surPourcent, "Tarte aux pommes", "Reduction 50% de sel")

	surSoulignement := rechercheDe(t, mux, cookie, "_")
	exigeSansAucun(t, surSoulignement, "Tarte aux pommes", ">A<")
}

// L'objet de PATA-31, et le test de PATA-13 retourné : celui-là constatait que
// « creme » ne ramenait pas « Crème brûlée ». L'index FTS5 replie les accents,
// et les deux graphies se retrouvent l'une l'autre.
func TestLaRechercheReplitLesAccentsDansLeTitre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Creme brulee"})
	creeRecette(t, app, recetteVoulue{titre: "Crème brûlée"})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	surSansAccent := rechercheDe(t, mux, cookie, "creme")
	exigeContient(t, surSansAccent, "Creme brulee", "Crème brûlée")
	exigeSansAucun(t, surSansAccent, "Soupe de potiron")

	surAccentue := rechercheDe(t, mux, cookie, "Crème")
	exigeContient(t, surAccentue, "Creme brulee", "Crème brûlée")
	exigeSansAucun(t, surAccentue, "Soupe de potiron")
}

// Trois sources, trois chemins d'indexation : le titre ne dit rien des deux
// autres. Un accent qui ne vit que dans une ligne d'ingrédient.
func TestLaRechercheReplitLesAccentsDansUneLigneDIngredient(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{
		titre:       "Gateau du dimanche",
		ingredients: []string{"200 g de farine", "20 cl de crème fraîche"},
	})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := rechercheDe(t, mux, cookie, "creme")

	exigeContient(t, corps, "Gateau du dimanche")
	exigeSansAucun(t, corps, "Soupe de potiron")
}

// Et un accent qui ne vit que dans un nom de tag.
func TestLaRechercheReplitLesAccentsDansUnNomDeTag(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Omelette", tags: []string{"végétarien"}})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := rechercheDe(t, mux, cookie, "vegetari")

	exigeContient(t, corps, "Omelette")
	exigeSansAucun(t, corps, "Soupe de potiron")
}

// Le prix de la bascule, et il est réel : FTS5 cherche des mots, pas des
// morceaux de mot. Le second cas n'est pas là par accident — il dit la limite.
func TestLaRechercheTrouveParPrefixeEtNonParSousChaine(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Crème brûlée"})

	exigeContient(t, rechercheDe(t, mux, cookie, "brûl"), "Crème brûlée")
	exigeSansAucun(t, rechercheDe(t, mux, cookie, "rûlée"), "Crème brûlée")
}

// Plusieurs mots deviennent un ET implicite, insensible à l'ordre — là où le
// LIKE exigeait la sous-chaîne exacte.
func TestPlusieursMotsSeCombinentQuelQueSoitLeurOrdre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{
		titre:       "Flan du dimanche",
		ingredients: []string{"1 gousse de vanille", "50 cl de creme"},
	})

	exigeContient(t, rechercheDe(t, mux, cookie, "creme vanille"), "Flan du dimanche")
	exigeContient(t, rechercheDe(t, mux, cookie, "vanille creme"), "Flan du dimanche")
	exigeSansAucun(t, rechercheDe(t, mux, cookie, "vanille chocolat"), "Flan du dimanche")
}

// unicode61 replie les accents mais ne décompose pas les ligatures. Limite
// connue et assumée, testée comme l'était celle des accents dans PATA-13.
func TestLaLigatureNEstPasDecomposee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{
		titre:       "Gateau du dimanche",
		ingredients: []string{"4 œufs"},
	})

	exigeContient(t, rechercheDe(t, mux, cookie, "œufs"), "Gateau du dimanche")
	exigeSansAucun(t, rechercheDe(t, mux, cookie, "oeufs"), "Gateau du dimanche")
}

// La syntaxe de requête de FTS5 est un langage : sans citation du terme, ces
// six formes rendent une erreur de syntaxe — donc une 500 — ou changent le
// sens de la requête. Citées, elles ne sont plus que des mots qu'aucune
// recette ne porte.
func TestLaSyntaxeFTS5NEstPasInterpretee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	for _, terme := range []string{"NEAR", "AND", "OR", "-creme", "*", `un"deux`} {
		t.Run(terme, func(t *testing.T) {
			rec := demande(mux, "/recettes?"+url.Values{"q": {terme}}.Encode(), cookie, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d pour le terme %q, attendu %d", rec.Code, terme, http.StatusOK)
			}

			corps := rec.Body.String()
			exigeSansAucun(t, corps, "Tarte aux pommes")
			if !strings.Contains(corps, "Aucune recette") {
				t.Errorf("pas de message d'absence de résultat pour %q :\n%s", terme, corps)
			}
		})
	}
}

// Une chaîne MATCH vide est une erreur de syntaxe FTS5 : un terme sans aucun
// mot ne doit produire aucun critère, et rendre le carnet entier comme un
// « q » absent.
func TestUnTermeReduitADesEspacesRendLeCarnetEntier(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})
	creeRecette(t, app, recetteVoulue{titre: "Soupe de potiron"})

	corps := rechercheDe(t, mux, cookie, "   ")

	exigeContient(t, corps, "Tarte aux pommes", "Soupe de potiron")
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

// Une page au-delà du dernier rang est une liste vide, pas une 500.
//
// 2305843009213693953 n'est pas un grand nombre pris au hasard : c'est 2^61+1,
// et (page-1)*24 y vaut exactement 3×2^64, donc zéro une fois débordé. Sans la
// borne posée sur le numéro de page, qui demande la dernière page du monde
// reçoit la première.
func TestUnePageAuDelaDuDernierRangEstVideSansErreur(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)

	for _, cible := range []string{"/recettes?page=3", "/recettes?page=2305843009213693953"} {
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

// --- Le filtre par type de plat --------------------------------------------

// typeDePlatEnBase ajoute un type de plat à la collection.
//
// La migration en sème huit, et typeDePlat les retrouve ; ces tests-ci ont
// besoin d'en poser d'autres — un libellé dangereux, un homonyme de la valeur
// réservée, un type ajouté après coup.
func typeDePlatEnBase(t *testing.T, app core.App, nom, slug string, position int) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("meal_types")
	if err != nil {
		t.Fatalf("collection meal_types : %v", err)
	}

	enregistrement := core.NewRecord(collection)
	enregistrement.Set("name", nom)
	enregistrement.Set("slug", slug)
	enregistrement.Set("position", position)
	if err := app.Save(enregistrement); err != nil {
		t.Fatalf("type de plat %q : %v", nom, err)
	}
	return enregistrement
}

// carnetClasse pose les trois recettes qui distinguent ce filtre de tout
// autre : une du type cherché, une d'un autre type, une sans type. Les trois à
// la fois, parce qu'un filtre écrit « … || meal_type = ” » ne se trahit qu'en
// présence de la troisième.
func carnetClasse(t *testing.T, app core.App) {
	t.Helper()

	creeRecette(t, app, recetteVoulue{titre: "Tarte au citron", typeDePlat: "Dessert"})
	creeRecette(t, app, recetteVoulue{titre: "Salade de chevre", typeDePlat: "Entrée"})
	creeRecette(t, app, recetteVoulue{titre: "Recette non classee"})
}

// Les trois blocs que cette tâche ajoute à la page, isolés du reste : les
// libellés des types apparaissent aussi bien dans la barre que sur les
// vignettes, et une assertion sur la page entière ne dirait pas lequel des
// chemins de rendu elle a éprouvé.
var (
	motifDeLaBarre          = regexp.MustCompile(`(?s)<nav class="types".*?</nav>`)
	motifDuFiltreActif      = regexp.MustCompile(`(?s)<p class="filtre-actif">.*?</p>`)
	motifDuTypeDeLaVignette = regexp.MustCompile(`(?s)<p class="type-de-plat">.*?</p>`)
)

func barreRendue(t *testing.T, corps string) string {
	t.Helper()

	barre := motifDeLaBarre.FindString(corps)
	if barre == "" {
		t.Fatalf("aucune barre de types dans la page :\n%s", corps)
	}
	return barre
}

func filtreActifRendu(corps string) string {
	return motifDuFiltreActif.FindString(corps)
}

func typeDeLaVignette(t *testing.T, corps string) string {
	t.Helper()

	rendu := motifDuTypeDeLaVignette.FindString(corps)
	if rendu == "" {
		t.Fatalf("aucun type de plat sur les vignettes :\n%s", corps)
	}
	return rendu
}

func TestLeFiltreParTypeNeRendQueLesRecettesDeCeType(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?type=dessert")

	exigeContient(t, corps, "Tarte au citron")
	exigeSansAucun(t, corps, "Salade de chevre", "Recette non classee")
}

func TestLaValeurSansRendLesRecettesNonClassees(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?type=sans")

	exigeContient(t, corps, "Recette non classee")
	exigeSansAucun(t, corps, "Tarte au citron", "Salade de chevre")
}

// « sans » est réservé, et c'est assumé : un type de plat dont le slug vaudrait
// « sans » n'est pas atteignable par le filtre. Le comportement ne dépend donc
// pas du contenu de la collection.
func TestSansEstReserveMemeSiUnTypeDePlatEnPorteLeSlug(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)
	homonyme := typeDePlatEnBase(t, app, "Sans-faute", "sans", 90)
	creeRecette(t, app, recetteVoulue{titre: "Recette homonyme", typeDePlatId: homonyme.Id})

	corps := listeDe(t, mux, cookie, "/recettes?type=sans")

	exigeContient(t, corps, "Recette non classee")
	exigeSansAucun(t, corps, "Recette homonyme", "Tarte au citron", "Salade de chevre")
}

// Un slug inconnu est une liste vide, pas une 500 et pas le carnet entier.
func TestUnSlugInconnuRendUneListeVideEtNonLeCarnet(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	rec := demande(mux, "/recettes?type=nexistepas", cookie, nil)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeSansAucun(t, corps, "Tarte au citron", "Salade de chevre", "Recette non classee")
	exigeContient(t, corps, "Aucune recette")
}

func TestUnTypeVideRendLeCarnetEntier(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	rec := demande(mux, "/recettes?type=", cookie, nil)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeContient(t, corps, "Tarte au citron", "Salade de chevre", "Recette non classee")
	if filtre := filtreActifRendu(corps); filtre != "" {
		t.Errorf("un filtre est annoncé alors qu'aucun n'est posé : %s", filtre)
	}
}

func TestLeTypeEtLaRechercheSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes", typeDePlat: "Dessert"})
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux oignons", typeDePlat: "Entrée"})

	// Sans le type, la recherche en ramène deux : c'est ce qui fait du
	// restreint à une un effet du filtre, et non du terme.
	exigeContient(t, listeDe(t, mux, cookie, "/recettes?q=tarte"), "Tarte aux pommes", "Tarte aux oignons")

	corps := listeDe(t, mux, cookie, "/recettes?q=tarte&type=dessert")

	exigeContient(t, corps, "Tarte aux pommes")
	exigeSansAucun(t, corps, "Tarte aux oignons")
}

func TestLeTypeEtLaPaginationSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	for i := 0; i < 25; i++ {
		creeRecette(t, app, recetteVoulue{titre: numero(i), typeDePlat: "Dessert", cree: instant(i)})
	}
	creeRecette(t, app, recetteVoulue{titre: "Cassoulet numero unique", typeDePlat: "Entrée", cree: instant(100)})

	premiere := listeDe(t, mux, cookie, "/recettes?q=numero&type=dessert")

	if compte := strings.Count(premiere, "Recette numero "); compte != 24 {
		t.Errorf("%d vignettes sur la première page, attendu 24", compte)
	}
	exigeSansAucun(t, premiere, "Cassoulet numero unique")
	exigeContient(t, premiere, `rel="next" href="/recettes?page=2&amp;q=numero&amp;type=dessert"`)

	seconde := listeDe(t, mux, cookie, "/recettes?q=numero&type=dessert&page=2")

	exigeContient(t, seconde, numero(0), `rel="prev" href="/recettes?q=numero&amp;type=dessert"`)
	exigeSansAucun(t, seconde, "Cassoulet numero unique")
}

// La barre suit position, pas l'alphabet : « Entrée » est semée en position 10
// et « Dessert » en 30, et l'ordre alphabétique les met dans l'autre sens.
func TestLaBarreSuitLaPositionEtNonLAlphabet(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	barre := barreRendue(t, listeDe(t, mux, cookie, "/recettes"))

	entree := strings.Index(barre, `href="/recettes?type=entree"`)
	dessert := strings.Index(barre, `href="/recettes?type=dessert"`)
	if entree < 0 || dessert < 0 {
		t.Fatalf("la barre ne porte pas les deux types :\n%s", barre)
	}
	if entree > dessert {
		t.Errorf("« Dessert » précède « Entrée » : la barre est triée par nom et non par position :\n%s", barre)
	}
}

// Ajouter un type dans la collection ajoute un lien, sans recompilation : c'est
// tout l'argument du choix d'une collection plutôt que d'un champ de schéma.
func TestLaBarreRendUnLienParEnregistrementDeLaCollection(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	avant := strings.Count(barreRendue(t, listeDe(t, mux, cookie, "/recettes")), "<a ")

	typeDePlatEnBase(t, app, "Brunch", "brunch", 95)

	barre := barreRendue(t, listeDe(t, mux, cookie, "/recettes"))
	if apres := strings.Count(barre, "<a "); apres != avant+1 {
		t.Errorf("%d liens après l'ajout d'un type, attendu %d :\n%s", apres, avant+1, barre)
	}
	exigeContient(t, barre, `href="/recettes?type=brunch"`, "Brunch")
}

func TestLaBarrePorteTousEtSansType(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	barre := barreRendue(t, listeDe(t, mux, cookie, "/recettes"))

	exigeContient(t, barre, `href="/recettes"`, "Tous", `href="/recettes?type=sans"`, "Sans type")
}

// Changer de filtre ramène au premier rang : rester sur la page 4 d'une autre
// liste n'a pas de sens. Le terme, lui, est conservé.
func TestLesLiensDeLaBarreGardentLeTermeEtPasLaPage(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	barre := barreRendue(t, listeDe(t, mux, cookie, "/recettes?q=tarte&page=2"))

	exigeContient(t, barre, `href="/recettes?q=tarte&amp;type=dessert"`, `href="/recettes?q=tarte"`)
	exigeSansAucun(t, barre, "page=")
}

// Un filtre posé d'un clic doit avoir une sortie — y compris quand la valeur
// reçue n'est pas reconnue.
func TestLeFiltreActifPorteUnLienDeRetrait(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	for _, cible := range []string{"/recettes?type=dessert", "/recettes?type=sans", "/recettes?type=nexistepas"} {
		filtre := filtreActifRendu(listeDe(t, mux, cookie, cible))
		if filtre == "" {
			t.Errorf("%s ne porte aucun lien de retrait", cible)
			continue
		}
		exigeContient(t, filtre, `href="/recettes"`)
	}
}

func TestSansTypeAucunLienDeRetraitNApparait(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	for _, cible := range []string{"/recettes", "/recettes?type="} {
		if filtre := filtreActifRendu(listeDe(t, mux, cookie, cible)); filtre != "" {
			t.Errorf("%s annonce un filtre : %s", cible, filtre)
		}
	}
}

func TestLeLienDeRetraitConserveLeTerme(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	filtre := filtreActifRendu(listeDe(t, mux, cookie, "/recettes?q=tarte&type=dessert&page=2"))

	exigeContient(t, filtre, `href="/recettes?q=tarte"`)
}

func TestLaPageNommeLeTypeFiltre(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	exigeContient(t, filtreActifRendu(listeDe(t, mux, cookie, "/recettes?type=entree")), "Entrée")
	exigeContient(t, filtreActifRendu(listeDe(t, mux, cookie, "/recettes?type=sans")), "Sans type de plat")
}

// Le code ne s'appuie ni sur le libellé ni sur l'identifiant PocketBase :
// renommer un type en base ne change ni ce que le filtre ramène ni l'adresse
// qui le porte, et seul le libellé affiché suit.
func TestUnTypeRenommeGardeSonSlugEtSesRecettes(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	dessert := typeDePlat(t, app, "Dessert")
	dessert.Set("name", "Desserts et douceurs")
	if err := app.Save(dessert); err != nil {
		t.Fatalf("renommage du type de plat : %v", err)
	}

	corps := listeDe(t, mux, cookie, "/recettes?type=dessert")

	exigeContient(t, corps, "Tarte au citron", "Desserts et douceurs")
	exigeSansAucun(t, corps, "Salade de chevre", "Recette non classee")
}

// Aucun slug de type de plat n'est écrit en dur dans le code de production : la
// seule valeur littérale admise est « sans », la valeur réservée.
func TestAucunSlugDeTypeDePlatNEstEnDurDansLeCode(t *testing.T) {
	app, _, _ := carnetDeTest(t)

	types, err := app.FindAllRecords("meal_types")
	if err != nil {
		t.Fatalf("lecture des types de plat : %v", err)
	}
	for _, type_ := range types {
		slug := type_.GetString("slug")
		if sources := chercheDansLesSources(t, `"`+slug+`"`); len(sources) > 0 {
			t.Errorf("le slug %q est écrit en dur dans %v", slug, sources)
		}
	}
}

// Sans ça, taper une lettre dans le champ de recherche annulerait le filtre
// qu'on vient de poser.
func TestLeChampDeRechercheEmporteLeType(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?type=dessert")

	exigeContient(t, corps, `hx-include="[name='tag'],[name='type'],[name='saison']"`, `name="type" value="dessert"`)

	// La requête que la page fait faire à HTMX, jouée telle quelle : le champ
	// de recherche part avec le type, et rend donc le fragment filtré.
	fragment := demande(mux, "/recettes?q=&type=dessert", cookie, map[string]string{"HX-Request": "true"}).Body.String()

	exigeContient(t, fragment, "Tarte au citron")
	exigeSansAucun(t, fragment, "Salade de chevre", "Recette non classee")
}

// Les deux critères de la liste posés ensemble. Chacun a ses tests ; ce que
// celui-ci couvre, c'est leur rencontre sur la même requête — la jointure du
// type et le résolveur de filtre de la saison, qui ne se connaissent pas.
//
// Les deux recettes écartées ne le sont chacune que par un seul des deux
// critères : perdre l'un ou l'autre filtre en ramène une, et rougit ici.
func TestLeTypeEtLaSaisonSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	creeRecette(t, app, recetteVoulue{titre: "Tarte aux fraises", typeDePlat: "Dessert", saisons: []string{"été"}})
	creeRecette(t, app, recetteVoulue{titre: "Sorbet au citron", typeDePlat: "Dessert", saisons: []string{"hiver"}})
	creeRecette(t, app, recetteVoulue{titre: "Salade de tomates", typeDePlat: "Entrée", saisons: []string{"été"}})

	corps := listeDe(t, mux, cookie, "/recettes?type=dessert&saison=ete")

	exigeContient(t, corps, "Tarte aux fraises")
	exigeSansAucun(t, corps, "Sorbet au citron", "Salade de tomates")
}

func TestHTMXSurUnTypeNeRecoitQueLeFragmentFiltre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	rec := demande(mux, "/recettes?type=dessert", cookie, map[string]string{"HX-Request": "true"})
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeSansAucun(t, corps, "<html", "<body", "Salade de chevre", "Recette non classee")
	exigeContient(t, corps, "Tarte au citron")
}

// Le critère de type ne doit pas ouvrir une porte que la liste a fermée.
func TestSansSessionLeFiltreParTypeNeRendRien(t *testing.T) {
	app, mux := serveurDeTest(t)
	carnetClasse(t, app)

	rec := demande(mux, "/recettes?type=dessert", nil, nil)

	if rec.Code != http.StatusFound {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusFound)
	}
	if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
		t.Errorf("Location %q, attendu %q", lieu, "/connexion")
	}
	exigeSansAucun(t, rec.Body.String(), "Tarte au citron")
}

// Les libellés se saisissent depuis l'administration : ce sont des données
// comme les autres. Trois emplacements les affichent — la barre, la vignette
// et la fiche —, donc trois tests d'échappement.
func TestLaBarreEchappeLeLibelleDUnType(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	typeDePlatEnBase(t, app, `"><script>alert(1)</script>`, "malveillant", 95)

	barre := barreRendue(t, listeDe(t, mux, cookie, "/recettes"))

	exigeSansAucun(t, barre, `"><script>`)
	exigeContient(t, barre, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestLaVignetteEchappeLeLibelleDuType(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	malveillant := typeDePlatEnBase(t, app, `"><script>alert(1)</script>`, "malveillant", 95)
	creeRecette(t, app, recetteVoulue{titre: "Tarte au citron", typeDePlatId: malveillant.Id})

	rendu := typeDeLaVignette(t, listeDe(t, mux, cookie, "/recettes"))

	exigeSansAucun(t, rendu, `"><script>`)
	exigeContient(t, rendu, "&lt;script&gt;alert(1)&lt;/script&gt;")
}

// La valeur reçue en chaîne de requête n'est jamais réémise : ni telle quelle,
// ni échappée, ni encodée dans une adresse. Une valeur inconnue n'a rien à
// faire dans la page, et le message d'absence générique suffit à le dire.
func TestLaValeurDeTypeNEstJamaisReemise(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetClasse(t, app)

	malveillante := `<script>alert(1)</script>`
	corps := listeDe(t, mux, cookie, "/recettes?"+url.Values{"type": {malveillante}, "page": {"3"}}.Encode())

	exigeSansAucun(t, corps,
		malveillante,
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		url.QueryEscape(malveillante),
	)
}

func TestLaVignetteRendLeTypeDePlatCliquable(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte au citron", typeDePlat: "Dessert"})

	rendu := typeDeLaVignette(t, listeDe(t, mux, cookie, "/recettes"))

	exigeContient(t, rendu, `href="/recettes?type=dessert"`, ">Dessert<")
}

func TestLaVignetteDUneRecetteSansTypeNeRendNiLienNiLibelle(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Pain perdu"})

	corps := listeDe(t, mux, cookie, "/recettes")

	if rendu := motifDuTypeDeLaVignette.FindString(corps); rendu != "" {
		t.Errorf("une vignette sans type de plat rend %q", rendu)
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

// --- Le filtre par saison (PATA-18) ----------------------------------------

// carnetDesSaisons sème les quatre cas que le filtre doit distinguer : une
// recette à deux saisons dont une seule correspondra, une recette d'une autre
// saison, et une recette sans aucune saison — celle des imports.
func carnetDesSaisons(t *testing.T, app core.App) {
	t.Helper()

	creeRecette(t, app, recetteVoulue{titre: "Soupe de courge", saisons: []string{"automne", "hiver"}})
	creeRecette(t, app, recetteVoulue{titre: "Salade de tomates", saisons: []string{"été"}})
	creeRecette(t, app, recetteVoulue{titre: "Pain perdu"})
}

// horlogeFixee remplace la couture de date le temps d'un test. Sans elle,
// « de saison » ne se testerait qu'au mois où le test tourne.
func horlogeFixee(t *testing.T, date time.Time) {
	t.Helper()

	precedente := horloge
	horloge = func() time.Time { return date }
	t.Cleanup(func() { horloge = precedente })
}

// titresPresents rend, dans l'ordre donné, ceux des titres que la page porte :
// c'est le jeu de recettes rendu, et c'est sur lui que deux listes se
// comparent.
func titresPresents(corps string, titres ...string) []string {
	presents := []string{}
	for _, titre := range titres {
		if strings.Contains(corps, titre) {
			presents = append(presents, titre)
		}
	}
	return presents
}

// Le cœur du filtre : « au moins une des saisons vaut ». Une recette d'automne
// et d'hiver sort en hiver. Écrit avec « seasons:each = », qui veut dire
// « toutes les saisons valent », elle disparaîtrait.
func TestLeFiltreRamneUneRecetteDontUneSeuleSaisonCorrespond(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?saison=hiver")

	exigeContient(t, corps, "Soupe de courge")
}

func TestLeFiltreEcarteUneRecetteDUneAutreSaison(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?saison=hiver")

	exigeSansAucun(t, corps, "Salade de tomates")
}

// « Aucune saison » vaut « toute l'année », et sort donc sous n'importe quelle
// saison. Les recettes importées arriveront toutes ainsi : un filtre qui les
// exclurait viderait le carnet. Les deux saisons, parce qu'un test qui ne
// porterait que sur l'une pourrait passer sur une comparaison de hasard.
func TestUneRecetteSansSaisonEstDeTouteLAnnee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	for _, cible := range []string{"/recettes?saison=hiver", "/recettes?saison=printemps"} {
		exigeContient(t, listeDe(t, mux, cookie, cible), "Pain perdu")
	}
}

// Le piège de l'opérateur : « seasons ~ {:s} » filtrerait par LIKE sur le JSON
// stocké, et « seasons ?= {:s} » sans :each ne ramènerait rien du tout.
func TestLeFiltreNeRapprochePasDeuxSaisonsDifferentes(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?saison=ete")

	exigeSansAucun(t, corps, "Soupe de courge")
}

// Sans accent dans l'URL, avec accent en base : c'est toute la raison d'être de
// la table.
func TestLaSaisonSEcritSansAccentDansLURL(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?saison=ete")

	exigeContient(t, corps, "Salade de tomates")
}

// « De saison » se cale sur la date du jour, et non sur une saison figée dans
// l'URL : la comparaison porte sur le jeu de recettes rendu, seul point où les
// deux listes doivent coïncider — l'URL du filtre, elle, reste « maintenant ».
func TestMaintenantVautLaSaisonDuJour(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)
	// La soupe porte automne et hiver : sur le seul carnet de la fixture, les
	// deux saisons rendent la même chose, et l'égalité ci-dessous se
	// vérifierait sous une horloge fausse. Cette recette-ci les sépare.
	creeRecette(t, app, recetteVoulue{titre: "Gratin de potiron", saisons: []string{"automne"}})
	horlogeFixee(t, time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC))

	const soupe, gratin, salade, pain = "Soupe de courge", "Gratin de potiron", "Salade de tomates", "Pain perdu"
	rendus := func(cible string) []string {
		return titresPresents(listeDe(t, mux, cookie, cible), soupe, gratin, salade, pain)
	}

	maintenant, automne, hiver := rendus("/recettes?saison=maintenant"), rendus("/recettes?saison=automne"), rendus("/recettes?saison=hiver")

	if !slices.Equal(maintenant, automne) {
		t.Errorf("« maintenant » en novembre rend %q, « automne » rend %q", maintenant, automne)
	}
	if slices.Equal(automne, hiver) {
		t.Fatalf("automne et hiver rendent tous deux %q : l'égalité ci-dessus ne prouverait rien", automne)
	}
}

// Une valeur hors table ne peut être qu'une URL tapée de travers : le critère
// est ignoré, le carnet entier est rendu, et le filtre s'affiche comme
// inactif. Même traitement que le « page » malmené de PATA-13.
func TestUneSaisonInconnueRendLeCarnetEntier(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	for _, cible := range []string{"/recettes?saison=nexistepas", "/recettes?saison="} {
		corps := listeDe(t, mux, cookie, cible)

		exigeContient(t, corps, "Soupe de courge", "Salade de tomates", "Pain perdu")
		if strings.Contains(corps, "filtre-saison") {
			t.Errorf("%s affiche un filtre actif :\n%s", cible, corps)
		}
	}
}

// Deux recettes que la recherche ramène, une seule saison partagée : c'est la
// combinaison des deux critères, et non l'un qui écraserait l'autre. Aucune
// des deux n'est sans saison, sinon elle sortirait dans tous les cas.
func TestLaSaisonEtLaRechercheSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes", saisons: []string{"automne"}})
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux fraises", saisons: []string{"été"}})

	corps := listeDe(t, mux, cookie, "/recettes?q=tarte&saison=automne")

	exigeContient(t, corps, "Tarte aux pommes")
	exigeSansAucun(t, corps, "Tarte aux fraises")
}

// La pagination s'écrit à partir des critères lus, et non d'une liste de
// paramètres recopiée à la main : c'est ce qui fait qu'un critère de plus
// n'oblige pas à la reprendre.
func TestLaSaisonEtLaPaginationSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	vingtCinqRecettes(t, app)
	creeRecette(t, app, recetteVoulue{titre: "Cassoulet", saisons: []string{"hiver"}, cree: instant(100)})

	corps := listeDe(t, mux, cookie, "/recettes?q=numero&saison=hiver")

	// Les vingt-cinq recettes de la fixture sont sans saison, donc de toute
	// l'année : elles restent paginées sous un filtre de saison.
	if compte := strings.Count(corps, "Recette numero "); compte != 24 {
		t.Errorf("%d vignettes, attendu 24", compte)
	}
	exigeContient(t, corps, `rel="next" href="/recettes?page=2&amp;q=numero&amp;saison=hiver"`)

	seconde := listeDe(t, mux, cookie, "/recettes?q=numero&saison=hiver&page=2")
	exigeContient(t, seconde, numero(0), `rel="prev" href="/recettes?q=numero&amp;saison=hiver"`)
}

// « De saison » doit tenir en un clic, et pointer « maintenant » plutôt qu'une
// saison figée : une URL mise en favori en décembre doit encore dire « de
// saison » en juin.
func TestLaListePorteLeLienDeSaison(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	exigeContient(t, listeDe(t, mux, cookie, "/recettes"), `href="/recettes?saison=maintenant"`)
	exigeContient(t, listeDe(t, mux, cookie, "/recettes?q=courge"),
		`href="/recettes?q=courge&amp;saison=maintenant"`)
}

// Un filtre posé d'un clic doit avoir une sortie, et se nommer sous son nom
// accentué — « été », pas « ete ».
func TestLeFiltreActifSeNommeEtSeRetire(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?q=salade&saison=ete")

	exigeContient(t, corps, "été", `<a href="/recettes?q=salade">Toutes les saisons</a>`)
}

func TestUnFiltreInconnuNOffrePasDeRetrait(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?q=salade&saison=nexistepas")

	exigeSansAucun(t, corps, "Toutes les saisons")
}

// Sans ça, taper une lettre dans le champ de recherche annulerait le filtre
// qu'on vient de poser. Le test ne se contente pas de lire l'attribut : il
// rejoue la requête que la page décrit, et vérifie qu'elle rend le fragment
// filtré.
func TestLaRechercheConserveLeFiltreDeSaison(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?saison=ete")
	exigeContient(t, corps, `hx-include="[name='tag'],[name='type'],[name='saison']"`, `name="saison"`, `value="ete"`)

	rec := demande(mux, "/recettes?q=&saison=ete", cookie, map[string]string{"HX-Request": "true"})
	fragment := rec.Body.String()

	exigeContient(t, fragment, "Salade de tomates")
	exigeSansAucun(t, fragment, "Soupe de courge")
}

func TestHTMXNeRecoitQueLeFragmentFiltre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	rec := demande(mux, "/recettes?saison=ete", cookie, map[string]string{"HX-Request": "true"})
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeSansAucun(t, corps, "<html", "<body", "Soupe de courge")
	exigeContient(t, corps, "Salade de tomates")
}

// Le critère de saison ne doit pas ouvrir une porte que PATA-13 a fermée
// (DOD.md §3).
func TestSansSessionLeFiltreDeSaisonNeRendRien(t *testing.T) {
	app, mux := serveurDeTest(t)
	carnetDesSaisons(t, app)

	rec := demande(mux, "/recettes?saison=hiver", nil, nil)

	if rec.Code != http.StatusFound {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusFound)
	}
	if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
		t.Errorf("Location %q, attendu %q", lieu, "/connexion")
	}
	exigeSansAucun(t, rec.Body.String(), "Soupe de courge", "Salade de tomates", "Pain perdu")
}

// La valeur ne ressort échappée nulle part parce qu'elle ne ressort pas du
// tout : une saison hors table est ignorée, et rien de ce paramètre n'est
// réaffiché (DOD.md §3).
func TestLaSaisonNEstJamaisReaffichee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	carnetDesSaisons(t, app)

	corps := listeDe(t, mux, cookie, "/recettes?"+url.Values{"saison": {`<script>alert(1)</script>`}}.Encode())

	exigeSansAucun(t, corps,
		"<script>alert(1)</script>",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
		"alert(1)",
	)
}

// La distinction que PATA-13 pose entre le carnet vide et l'absence de
// résultat vaut pour le nouveau critère : un filtre de saison qui ne ramène
// rien n'est pas un carnet vide, et proposer « ajoutez votre première recette »
// à qui en a déjà dix serait aussi faux ici que sur une recherche.
func TestUnFiltreSansResultatNEstPasUnCarnetVide(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	creeRecette(t, app, recetteVoulue{titre: "Salade de tomates", saisons: []string{"été"}})

	corps := listeDe(t, mux, cookie, "/recettes?saison=hiver")

	exigeContient(t, corps, "Aucune recette")
	if strings.Contains(corps, "carnet est vide") {
		t.Errorf("l'invitation du carnet vide s'affiche sous un filtre de saison :\n%s", corps)
	}
}

// --- Le formulaire de création et d'édition --------------------------------

// --- Le montage ------------------------------------------------------------

// fichierPoste porte un téléversement : le nom que le navigateur annonce et
// les octets envoyés. Le type MIME n'y figure pas — c'est le contenu qui le
// décide, comme chez PocketBase.
type fichierPoste struct {
	nom     string
	contenu []byte
}

// poste envoie le formulaire en multipart, qui est ce qu'envoie un formulaire
// portant un <input type="file"> : tester en urlencodé exercerait un décodage
// que le navigateur n'emprunte jamais.
func poste(t *testing.T, mux http.Handler, cible string, cookie *http.Cookie, champs url.Values, fichiers ...fichierPoste) *httptest.ResponseRecorder {
	t.Helper()

	corps := &bytes.Buffer{}
	ecrivain := multipart.NewWriter(corps)
	for nom, valeurs := range champs {
		for _, valeur := range valeurs {
			if err := ecrivain.WriteField(nom, valeur); err != nil {
				t.Fatalf("champ %q : %v", nom, err)
			}
		}
	}
	for _, fichier := range fichiers {
		partie, err := ecrivain.CreateFormFile("image", fichier.nom)
		if err != nil {
			t.Fatalf("fichier %q : %v", fichier.nom, err)
		}
		if _, err := partie.Write(fichier.contenu); err != nil {
			t.Fatalf("écriture de %q : %v", fichier.nom, err)
		}
	}
	if err := ecrivain.Close(); err != nil {
		t.Fatalf("clôture du corps multipart : %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, cible, corps)
	req.Header.Set("Content-Type", ecrivain.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// champsValides rend une saisie complète et correcte : les tests qui portent
// sur un seul champ partent de là et n'en changent qu'un.
func champsValides() url.Values {
	return url.Values{
		"titre":             {"Tarte aux pommes"},
		"portions":          {"6"},
		"temps-preparation": {"20"},
		"temps-cuisson":     {"40"},
		"instructions":      {"Éplucher, puis enfourner."},
		"ingredients":       {"500 g de farine\n3 pommes"},
		"tags":              {"dessert, pommes"},
		"saisons":           {"automne", "hiver"},
	}
}

// pngDeTest rend une image PNG réelle : le type MIME se lit dans les octets,
// pas dans le nom du fichier, donc un contenu bidon serait refusé pour la
// mauvaise raison.
func pngDeTest(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 200, G: 120, B: 40, A: 255})

	corps := &bytes.Buffer{}
	if err := png.Encode(corps, img); err != nil {
		t.Fatalf("encodage du PNG de test : %v", err)
	}
	return corps.Bytes()
}

// recettes rend toutes les recettes enregistrées.
func recettes(t *testing.T, app core.App) []*core.Record {
	t.Helper()

	trouvees, err := app.FindAllRecords("recipes")
	if err != nil {
		t.Fatalf("lecture des recettes : %v", err)
	}
	return trouvees
}

// laRecette rend l'unique recette enregistrée, ou fait échouer le test.
func laRecette(t *testing.T, app core.App) *core.Record {
	t.Helper()

	trouvees := recettes(t, app)
	if len(trouvees) != 1 {
		t.Fatalf("%d recettes enregistrées, une seule attendue", len(trouvees))
	}
	return trouvees[0]
}

// relitLaRecette relit la recette depuis la base : une édition se juge sur ce
// qui est écrit, pas sur l'enregistrement que le test tient déjà.
func relitLaRecette(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()

	recette, err := app.FindRecordById("recipes", id)
	if err != nil {
		t.Fatalf("relecture de la recette %s : %v", id, err)
	}
	return recette
}

// lignesDe rend les raw des ingrédients de la recette, dans l'ordre des
// position.
func lignesDe(t *testing.T, app core.App, recette *core.Record) []string {
	t.Helper()

	brutes := []string{}
	for _, ligne := range ingredientsDe(t, app, recette) {
		brutes = append(brutes, ligne.GetString("raw"))
	}
	return brutes
}

// ingredientsDe rend les ingrédients de la recette, triés par position.
func ingredientsDe(t *testing.T, app core.App, recette *core.Record) []*core.Record {
	t.Helper()

	lignes, err := app.FindAllRecords("ingredients", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		t.Fatalf("lecture des ingrédients : %v", err)
	}
	sort.SliceStable(lignes, func(i, j int) bool {
		return lignes[i].GetInt("position") < lignes[j].GetInt("position")
	})
	return lignes
}

// recetteEnregistree pose une recette directement en base, sans passer par le
// formulaire : c'est l'état de départ des tests d'édition.
func recetteEnregistree(t *testing.T, app core.App, champs map[string]any) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", "Tarte aux pommes")
	for nom, valeur := range champs {
		recette.Set(nom, valeur)
	}
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette : %v", err)
	}
	return recette
}

// poseLesIngredients rattache les lignes à la recette, dans l'ordre donné.
func poseLesIngredients(t *testing.T, app core.App, recette *core.Record, brutes ...string) {
	t.Helper()

	for i, brut := range brutes {
		ligne := ingredientNeuf(t, app, recette, brut)
		ligne.Set("position", i+1)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}
}

// --- Session : le sens du refus -------------------------------------------

// Les quatre routes sont des routes d'écriture ou d'accès à des recettes :
// sans session, elles ne rendent rien et renvoient à la page de connexion.
// C'est le critère qui passe avant tous les autres — il s'évalue avant même
// la recherche de la recette en édition.
func TestLesQuatreRoutesRefusentUnVisiteur(t *testing.T) {
	app, mux, _ := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	cas := []struct {
		nom     string
		methode string
		cible   string
	}{
		{"formulaire de création", http.MethodGet, "/recettes/nouvelle"},
		{"création", http.MethodPost, "/recettes"},
		{"formulaire d'édition", http.MethodGet, "/recettes/" + recette.Id + "/modifier"},
		{"édition", http.MethodPost, "/recettes/" + recette.Id},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if c.methode == http.MethodGet {
				rec = avecCookie(mux, c.methode, c.cible, nil)
			} else {
				rec = poste(t, mux, c.cible, nil, champsValides())
			}

			if rec.Code != http.StatusSeeOther {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
			}
			if destination := rec.Header().Get("Location"); destination != "/connexion" {
				t.Errorf("Location %q, attendu %q", destination, "/connexion")
			}
			if corps := rec.Body.String(); strings.Contains(corps, "<form") {
				t.Errorf("un formulaire a été rendu à un visiteur :\n%s", corps)
			}
		})
	}
}

// Un refus qui répondrait bien mais écrirait quand même ne protégerait rien :
// les deux POST se vérifient sur la base, pas sur la réponse.
func TestUnPostSansSessionNecritRien(t *testing.T) {
	app, mux, _ := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{"title": "Titre d'origine"})
	poseLesIngredients(t, app, recette, "500 g de farine")

	champs := champsValides()
	champs.Set("titre", "Titre injecté")

	poste(t, mux, "/recettes", nil, champs)
	if n := len(recettes(t, app)); n != 1 {
		t.Errorf("%d recettes après une création sans session, 1 attendue", n)
	}

	poste(t, mux, "/recettes/"+recette.Id, nil, champs)
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Titre d'origine" {
		t.Errorf("titre %q après une édition sans session, %q attendu", titre, "Titre d'origine")
	}
	if lignes := lignesDe(t, app, recette); len(lignes) != 1 || lignes[0] != "500 g de farine" {
		t.Errorf("ingrédients %q après une édition sans session, inchangés attendus", lignes)
	}
}

// Le sens de l'autorisation, sans quoi le critère précédent serait satisfait
// par quatre routes cassées.
func TestLesQuatreRoutesServentUnCompteConnecte(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	if rec := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie); rec.Code != http.StatusOK {
		t.Errorf("GET /recettes/nouvelle : statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if rec := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie); rec.Code != http.StatusOK {
		t.Errorf("GET /recettes/{id}/modifier : statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	rec := poste(t, mux, "/recettes", cookie, champsValides())
	if rec.Code != http.StatusSeeOther {
		t.Errorf("POST /recettes : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if n := len(recettes(t, app)); n != 2 {
		t.Errorf("%d recettes après la création, 2 attendues", n)
	}

	rec = poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides())
	if rec.Code != http.StatusSeeOther {
		t.Errorf("POST /recettes/{id} : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Tarte aux pommes" {
		t.Errorf("titre %q après l'édition, %q attendu", titre, "Tarte aux pommes")
	}
}

// --- Le formulaire ---------------------------------------------------------

func TestLeFormulaireNeufPorteLesDixChamps(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	champs := []string{
		"titre", "image", "portions", "temps-preparation", "temps-cuisson",
		"instructions", "ingredients", "tags", "type-de-plat", "saisons",
	}
	for _, champ := range champs {
		if !strings.Contains(corps, `name="`+champ+`"`) {
			t.Errorf("formulaire sans le champ %q :\n%s", champ, corps)
		}
	}
	if !strings.Contains(corps, `action="/recettes"`) {
		t.Errorf("formulaire sans action de création :\n%s", corps)
	}
	// Sans enctype, un navigateur poste le seul nom du fichier : le champ
	// image serait rendu et pourtant inopérant.
	if !strings.Contains(corps, `enctype="multipart/form-data"`) {
		t.Errorf("formulaire sans enctype multipart :\n%s", corps)
	}
}

// Le type de plat vient de meal_types, ordonné par position : un tri
// alphabétique mettrait « Dessert » avant « Entrée ».
func TestLeFormulaireOffreLesTypesDePlatDansLOrdre(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie).Body.String()

	precedent := -1
	for _, nom := range []string{"Entrée", "Plat", "Dessert", "Apéritif"} {
		position := strings.Index(corps, nom)
		if position < 0 {
			t.Fatalf("type de plat %q absent du formulaire :\n%s", nom, corps)
		}
		if position < precedent {
			t.Errorf("type de plat %q rendu hors de l'ordre des position :\n%s", nom, corps)
		}
		precedent = position
	}
}

func TestLeFormulaireOffreLesQuatreSaisons(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie).Body.String()

	for _, saison := range []string{"printemps", "été", "automne", "hiver"} {
		if !strings.Contains(corps, `value="`+saison+`"`) {
			t.Errorf("saison %q absente du formulaire :\n%s", saison, corps)
		}
	}
}

// --- La création -----------------------------------------------------------

func TestUnPostValideCreeLaRecetteEtSesIngredients(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	champs := champsValides()
	champs.Set("ingredients", "  500 g de farine  \n\n   \n3 pommes\nune pincée de sel")

	rec := poste(t, mux, "/recettes", cookie, champs)

	recette := laRecette(t, app)
	if destination := rec.Header().Get("Location"); destination != "/recettes/"+recette.Id {
		t.Errorf("Location %q, attendu %q", destination, "/recettes/"+recette.Id)
	}
	if titre := recette.GetString("title"); titre != "Tarte aux pommes" {
		t.Errorf("titre %q, attendu %q", titre, "Tarte aux pommes")
	}
	if portions := recette.GetInt("servings"); portions != 6 {
		t.Errorf("portions %d, attendues 6", portions)
	}
	if preparation := recette.GetInt("prep_time"); preparation != 20 {
		t.Errorf("temps de préparation %d, attendu 20", preparation)
	}
	if cuisson := recette.GetInt("cook_time"); cuisson != 40 {
		t.Errorf("temps de cuisson %d, attendu 40", cuisson)
	}
	if saisons := recette.GetStringSlice("seasons"); strings.Join(saisons, ",") != "automne,hiver" {
		t.Errorf("saisons %q, attendues [automne hiver]", saisons)
	}
	if tags := recette.GetStringSlice("tags"); len(tags) != 2 {
		t.Errorf("%d tags, 2 attendus", len(tags))
	}

	// Les lignes vides ou blanches ne font pas d'ingrédient, et les espaces
	// de bout sont rognés : raw est la ligne saisie, pas la ligne tapée.
	attendues := []string{"500 g de farine", "3 pommes", "une pincée de sel"}
	lignes := ingredientsDe(t, app, recette)
	if len(lignes) != len(attendues) {
		t.Fatalf("%d ingrédients, %d attendus : %q", len(lignes), len(attendues), lignesDe(t, app, recette))
	}
	for i, ligne := range lignes {
		if brut := ligne.GetString("raw"); brut != attendues[i] {
			t.Errorf("raw %q en %d, attendu %q", brut, i, attendues[i])
		}
		// Une position par ligne enregistrée, sans trou : une ligne blanche
		// sautée ne doit pas laisser de creux dans la numérotation.
		if position := ligne.GetInt("position"); position != i+1 {
			t.Errorf("position %d pour %q, attendue %d", position, ligne.GetString("raw"), i+1)
		}
	}
}

// created_by est posé depuis la session, jamais depuis le formulaire.
func TestUnePostValideAttribueLaRecetteAuCompteConnecte(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("compte de test : %v", err)
	}

	poste(t, mux, "/recettes", cookie, champsValides())

	if auteur := laRecette(t, app).GetString(champAuteur); auteur != compte.Id {
		t.Errorf("created_by %q, attendu %q", auteur, compte.Id)
	}
}

func TestUnPostSansTitreNeCreeRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	champs := champsValides()
	champs.Set("titre", "   ")

	rec := poste(t, mux, "/recettes", cookie, champs)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if n := len(recettes(t, app)); n != 0 {
		t.Errorf("%d recettes créées malgré le titre manquant, 0 attendue", n)
	}
	if n := nombreDIngredients(t, app); n != 0 {
		t.Errorf("%d ingrédients créés malgré le titre manquant, 0 attendu", n)
	}
	if !strings.Contains(strings.ToLower(corps), "titre") {
		t.Errorf("message d'erreur sans le nom du champ fautif :\n%s", corps)
	}
	// L'utilisateur ne retape rien : ce qu'il avait saisi lui revient.
	for _, saisi := range []string{"500 g de farine", "3 pommes", "Éplucher, puis enfourner."} {
		if !strings.Contains(corps, saisi) {
			t.Errorf("le formulaire re-rendu a perdu %q :\n%s", saisi, corps)
		}
	}
}

// --- L'édition -------------------------------------------------------------

func TestLeFormulaireDEditionEstPreRempli(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{
		"title":        "Clafoutis",
		"servings":     4,
		"prep_time":    15,
		"cook_time":    35,
		"instructions": "Mélanger.",
		"seasons":      []string{"été"},
	})
	poseLesIngredients(t, app, recette, "500 g de cerises", "3 œufs", "80 g de sucre")

	rec := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(corps, `action="/recettes/`+recette.Id+`"`) {
		t.Errorf("formulaire sans action d'édition :\n%s", corps)
	}
	for _, valeur := range []string{`value="Clafoutis"`, `value="4"`, `value="15"`, `value="35"`, "Mélanger."} {
		if !strings.Contains(corps, valeur) {
			t.Errorf("formulaire sans %q :\n%s", valeur, corps)
		}
	}
	// Les ingrédients reviennent un par ligne, dans l'ordre des position, à
	// partir de raw : c'est le texte que l'utilisateur avait saisi.
	if !strings.Contains(corps, "500 g de cerises\n3 œufs\n80 g de sucre") {
		t.Errorf("ingrédients non restitués un par ligne dans l'ordre :\n%s", corps)
	}
}

func TestUneEditionRemplaceLesIngredientsEnBloc(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	poseLesIngredients(t, app, recette, "500 g de cerises", "3 œufs", "80 g de sucre")

	champs := champsValides()
	champs.Set("ingredients", "400 g de framboises\n3 œufs")
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	lignes := lignesDe(t, app, recette)
	if strings.Join(lignes, "|") != "400 g de framboises|3 œufs" {
		t.Errorf("ingrédients %q, attendus [400 g de framboises 3 œufs]", lignes)
	}
	// Une ligne restée de l'état précédent est un doublon invisible : les
	// ingrédients de la base entière comptent, pas seulement ceux de la
	// recette.
	if n := nombreDIngredients(t, app); n != 2 {
		t.Errorf("%d ingrédients en base, 2 attendus : des lignes de l'état précédent ont survécu", n)
	}
}

// Critère 3 : corriger une recette importée — y compris venue d'un import en
// lot — ne doit pas lui faire perdre son origine ni son auteur. L'origine
// traverse désormais le formulaire et revient telle quelle ; l'auteur, lui,
// n'est porté par aucun champ.
func TestUneEditionConserveLOrigineEtLAuteur(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	recette := recetteEnregistree(t, app, map[string]any{
		"source_url":  adresseDeSource,
		"source_name": "Exemple",
		champAuteur:   autre.Id,
	})

	poste(t, mux, "/recettes/"+recette.Id, cookie, champsAvecSource(adresseDeSource, "Exemple"))

	relue := relitLaRecette(t, app, recette.Id)
	if source := relue.GetString("source_url"); source != adresseDeSource {
		t.Errorf("source_url %q, attendue %q", source, adresseDeSource)
	}
	if nom := relue.GetString("source_name"); nom != "Exemple" {
		t.Errorf("source_name %q, attendu %q", nom, "Exemple")
	}
	if auteur := relue.GetString(champAuteur); auteur != autre.Id {
		t.Errorf("created_by %q, attendu %q", auteur, autre.Id)
	}
}

// Un champ created_by posté est falsifiable par nature : il n'est jamais lu,
// et le formulaire ne le porte pas.
func TestCreatedByNestJamaisLuDeLaRequete(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	recette := recetteEnregistree(t, app, map[string]any{champAuteur: autre.Id})

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()
	if strings.Contains(corps, champAuteur) {
		t.Errorf("le formulaire porte un champ %q :\n%s", champAuteur, corps)
	}

	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("compte de test : %v", err)
	}
	champs := champsValides()
	champs.Set(champAuteur, compte.Id)
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	if auteur := relitLaRecette(t, app, recette.Id).GetString(champAuteur); auteur != autre.Id {
		t.Errorf("created_by %q après un POST qui l'a proposé, %q attendu", auteur, autre.Id)
	}
}

// --- L'image ---------------------------------------------------------------

func TestUneEditionSansFichierConserveLImage(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	image := recette.GetString("image")
	if image == "" {
		t.Fatalf("aucune image enregistrée à la création")
	}

	poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides())

	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != image {
		t.Errorf("image %q après une édition sans fichier, %q attendue", apres, image)
	}
}

func TestUneCaseRetirerLImageLEfface(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	if recette.GetString("image") == "" {
		t.Fatalf("aucune image enregistrée à la création")
	}

	champs := champsValides()
	champs.Set("retirer-image", "1")
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != "" {
		t.Errorf("image %q après « retirer l'image », vide attendue", apres)
	}
}

// Un navigateur envoie la partie « image » même quand aucun fichier n'a été
// choisi : elle porte un nom vide et zéro octet.
//
// Le code ne s'en défend pas lui-même — il s'appuie sur mime/multipart, qui ne
// range dans les fichiers que les parties portant un nom (ReadForm). C'est
// exactement pour ça que ce test existe : l'hypothèse est invisible dans le
// code, et sa chute ferait échouer l'édition la plus ordinaire, celle où l'on
// ne touche pas à l'image.
func TestUneEditionAvecUnePartieDImageVideConserveLImage(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	poste(t, mux, "/recettes", cookie, champsValides(), fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})
	recette := laRecette(t, app)
	image := recette.GetString("image")
	if image == "" {
		t.Fatalf("aucune image enregistrée à la création")
	}

	rec := poste(t, mux, "/recettes/"+recette.Id, cookie, champsValides(), fichierPoste{})

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if apres := relitLaRecette(t, app, recette.Id).GetString("image"); apres != image {
		t.Errorf("image %q après une édition sans fichier choisi, %q attendue", apres, image)
	}
}

// Les deux bornes du schéma se voient dans le formulaire, pas dans une 500 :
// un téléversement refusé est une erreur de saisie ordinaire.
func TestUnTeleversementRefuseNeCreeRien(t *testing.T) {
	trop := append(pngDeTest(t), make([]byte, 6<<20)...)

	cas := []struct {
		nom     string
		fichier fichierPoste
	}{
		{"au-delà de 5 Mio", fichierPoste{nom: "enorme.png", contenu: trop}},
		{"type MIME hors liste", fichierPoste{nom: "notes.txt", contenu: []byte("ceci n'est pas une image")}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)

			rec := poste(t, mux, "/recettes", cookie, champsValides(), c.fichier)
			corps := rec.Body.String()

			if rec.Code != http.StatusOK {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
			}
			if n := len(recettes(t, app)); n != 0 {
				t.Errorf("%d recettes créées malgré le téléversement refusé, 0 attendue", n)
			}
			if n := nombreDIngredients(t, app); n != 0 {
				t.Errorf("%d ingrédients créés malgré le téléversement refusé, 0 attendu", n)
			}
			if !strings.Contains(corps, "<form") {
				t.Errorf("le formulaire n'a pas été re-rendu :\n%s", corps)
			}
			if !strings.Contains(strings.ToLower(corps), "image") {
				t.Errorf("message d'erreur sans le nom du champ fautif :\n%s", corps)
			}
		})
	}
}

// Le schéma borne une recette à vingt tags : au-delà, la saisie est refusée
// comme une erreur de saisie ordinaire, et non par une erreur serveur qui
// perdrait tout ce que l'utilisateur avait tapé.
func TestTropDeTagsNeCreeRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	trop := make([]string, maxTags+1)
	for i := range trop {
		trop[i] = fmt.Sprintf("tag-%d", i)
	}
	champs := champsValides()
	champs.Set("tags", strings.Join(trop, ","))

	rec := poste(t, mux, "/recettes", cookie, champs)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if n := len(recettes(t, app)); n != 0 {
		t.Errorf("%d recettes créées malgré les tags en trop, 0 attendue", n)
	}
	if !strings.Contains(corps, "<form") {
		t.Errorf("le formulaire n'a pas été re-rendu :\n%s", corps)
	}
	if !strings.Contains(strings.ToLower(corps), "tags") {
		t.Errorf("message d'erreur sans le nom du champ fautif :\n%s", corps)
	}
}

// raw n'a pas de borne explicite au schéma : PocketBase lui applique la
// sienne. Une ligne collée sans retour la dépasse, et c'est une faute de
// saisie ordinaire — le formulaire revient, il ne part pas en 500.
func TestUneLigneDIngredientTropLongueNeCreeRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	champs := champsValides()
	champs.Set("ingredients", "500 g de farine\n"+ligneTropLongue())

	rec := poste(t, mux, "/recettes", cookie, champs)
	corps := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if n := len(recettes(t, app)); n != 0 {
		t.Errorf("%d recettes créées malgré la ligne trop longue, 0 attendue", n)
	}
	if n := nombreDIngredients(t, app); n != 0 {
		t.Errorf("%d ingrédients créés malgré la ligne trop longue, 0 attendu", n)
	}
	if message := messageDErreur(t, corps); !strings.Contains(strings.ToLower(message), "ingrédient") {
		t.Errorf("message d'erreur %q, attendu nommant le champ fautif", message)
	}
	// L'utilisateur ne retape rien : ce qu'il avait saisi lui revient.
	for _, saisi := range []string{"Tarte aux pommes", "Éplucher, puis enfourner.", "500 g de farine"} {
		if !strings.Contains(corps, saisi) {
			t.Errorf("le formulaire re-rendu a perdu %q :\n%s", saisi, corps)
		}
	}
}

// Le même refus en édition : la recette et sa liste d'ingrédients doivent
// sortir de là exactement comme elles y sont entrées.
func TestUneEditionAvecUneLigneTropLongueNeChangeRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{"title": "Clafoutis"})
	poseLesIngredients(t, app, recette, "500 g de cerises", "3 œufs")

	champs := champsValides()
	champs.Set("ingredients", ligneTropLongue())

	rec := poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if titre := relitLaRecette(t, app, recette.Id).GetString("title"); titre != "Clafoutis" {
		t.Errorf("titre %q, attendu « Clafoutis » : la recette a été modifiée malgré le refus", titre)
	}
	if lignes := lignesDe(t, app, recette); strings.Join(lignes, "|") != "500 g de cerises|3 œufs" {
		t.Errorf("ingrédients %q, attendus [500 g de cerises 3 œufs] : la liste a été touchée", lignes)
	}
	if message := messageDErreur(t, rec.Body.String()); !strings.Contains(strings.ToLower(message), "ingrédient") {
		t.Errorf("message d'erreur %q, attendu nommant le champ fautif", message)
	}
}

// ligneTropLongue rend une ligne d'ingrédient au-delà de ce que le schéma
// accepte pour raw — un copier-coller sans retour à la ligne suffit.
func ligneTropLongue() string {
	return strings.Repeat("a", 5001)
}

// messageDErreur extrait le message d'alerte du document rendu. Le lire dans
// son paragraphe plutôt que dans le corps entier évite de le confondre avec
// les libellés du formulaire, qui nomment les mêmes champs.
func messageDErreur(t *testing.T, corps string) string {
	t.Helper()

	trouve := alerteRendue.FindStringSubmatch(corps)
	if trouve == nil {
		return ""
	}
	return trouve[1]
}

var alerteRendue = regexp.MustCompile(`(?s)<p class="erreur" role="alert">(.*?)</p>`)

// --- La source d'origine (PATA-86) -----------------------------------------

// adresseDeSource est l'adresse que ces tests font traverser le formulaire.
const adresseDeSource = "https://exemple.fr/tarte-aux-pommes"

// champsAvecSource rend la saisie valide, augmentée des deux champs de source
// tels que le formulaire les poste.
func champsAvecSource(adresse, nom string) url.Values {
	champs := champsValides()
	champs.Set("source-url", adresse)
	champs.Set("source-nom", nom)
	return champs
}

// baliseDuChamp rend la balise <input> qui porte ce nom, telle que le gabarit
// l'a écrite : c'est sur ses attributs que se juge « visible » ou « caché ».
func baliseDuChamp(t *testing.T, corps, nom string) string {
	t.Helper()

	motif := regexp.MustCompile(`<input[^>]*name="` + regexp.QuoteMeta(nom) + `"[^>]*>`)
	trouve := motif.FindString(corps)
	if trouve == "" {
		t.Fatalf("champ %q introuvable dans la réponse :\n%s", nom, corps)
	}
	return trouve
}

// Déroulé n° 1 : l'adresse de la source cesse d'être un champ caché réservé à
// l'import. Le même gabarit sert la création et l'édition, donc les deux
// pages portent le champ et son libellé.
func TestLeFormulairePorteUnChampDeSourceVisible(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{"source_url": adresseDeSource})

	cas := map[string]string{
		"création": "/recettes/nouvelle",
		"édition":  "/recettes/" + recette.Id + "/modifier",
	}
	for nom, cible := range cas {
		t.Run(nom, func(t *testing.T) {
			corps := avecCookie(mux, http.MethodGet, cible, cookie).Body.String()

			if champ := baliseDuChamp(t, corps, "source-url"); strings.Contains(champ, `type="hidden"`) {
				t.Errorf("le champ de source est encore caché : %s", champ)
			}
			exigeContient(t, corps, `<label for="source-url">`, "Source")
		})
	}
}

// Déroulé n° 2 : le nom du site ne se saisit pas — rien ne le tape à la main,
// il ne vient que de l'import. Il reste donc un champ caché.
func TestLeNomDeLaSourceResteUnChampCache(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{
		"source_url":  adresseDeSource,
		"source_name": "Exemple",
	})

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()

	if champ := baliseDuChamp(t, corps, "source-nom"); !strings.Contains(champ, `type="hidden"`) {
		t.Errorf("le nom de la source est devenu saisissable : %s", champ)
	}
}

// Critère 1 : une adresse tapée à la création se retrouve en base, et la fiche
// affiche le bloc de source qui n'attendait qu'elle.
func TestUneSourceSaisieALaCreationEstEnregistreeEtAffichee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	champs := champsValides()
	champs.Set("source-url", adresseDeSource)
	poste(t, mux, "/recettes", cookie, champs)

	recette := laRecette(t, app)
	if source := recette.GetString("source_url"); source != adresseDeSource {
		t.Fatalf("source_url %q, attendue %q", source, adresseDeSource)
	}

	corps := fiche(mux, cookie, recette.Id).Body.String()
	if !strings.Contains(corps, `<a href="`+adresseDeSource+`"`) {
		t.Errorf("la fiche n'affiche pas le lien de source :\n%s", corps)
	}
}

// Critère 2 : valider un import unitaire, c'est poster le formulaire que
// l'import a pré-rempli — les deux champs de source compris.
func TestValiderUnImportEnregistreLAdresseEtLeNomDuSite(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	poste(t, mux, "/recettes", cookie, champsAvecSource(adresseDeSource, "Exemple"))

	recette := laRecette(t, app)
	if source := recette.GetString("source_url"); source != adresseDeSource {
		t.Errorf("source_url %q, attendue %q", source, adresseDeSource)
	}
	if nom := recette.GetString("source_name"); nom != "Exemple" {
		t.Errorf("source_name %q, attendu %q", nom, "Exemple")
	}
}

// Critère 3 : rouvrir le formulaire d'édition montre l'adresse enregistrée.
// Sans ce pré-remplissage, réenregistrer la recette effacerait sa source.
func TestLeFormulaireDEditionPrerempliLaSource(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{
		"source_url":  adresseDeSource,
		"source_name": "Exemple",
	})

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()

	if source := valeurDe(t, corps, "source-url"); source != adresseDeSource {
		t.Errorf("champ source-url %q, attendu %q", source, adresseDeSource)
	}
	if nom := valeurDe(t, corps, "source-nom"); nom != "Exemple" {
		t.Errorf("champ source-nom %q, attendu %q", nom, "Exemple")
	}
}

// Critère 4 : changer l'adresse vide le nom du site. Un « Exemple » qui
// survivrait au changement ferait un lien qui ment sur sa destination ; le
// repli sur l'hôte reprend la main à l'affichage.
func TestChangerLAdresseDeSourceVideLeNomDuSite(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{
		"source_url":  adresseDeSource,
		"source_name": "Exemple",
	})

	const nouvelle = "https://autre-site.fr/la-vraie-recette"
	poste(t, mux, "/recettes/"+recette.Id, cookie, champsAvecSource(nouvelle, "Exemple"))

	relue := relitLaRecette(t, app, recette.Id)
	if source := relue.GetString("source_url"); source != nouvelle {
		t.Errorf("source_url %q, attendue %q", source, nouvelle)
	}
	if nom := relue.GetString("source_name"); nom != "" {
		t.Errorf("source_name %q, attendu vide : il désigne le site de l'ancienne adresse", nom)
	}
}

// Critère 5 : vider le champ efface la source. Le bloc disparaît alors de la
// fiche, le nom du site étant vidé avec l'adresse.
func TestViderLeChampDeSourceEffaceLaSource(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{
		"source_url":  adresseDeSource,
		"source_name": "Exemple",
	})

	poste(t, mux, "/recettes/"+recette.Id, cookie, champsAvecSource("", "Exemple"))

	relue := relitLaRecette(t, app, recette.Id)
	if source := relue.GetString("source_url"); source != "" {
		t.Errorf("source_url %q, attendue vide", source)
	}
	if nom := relue.GetString("source_name"); nom != "" {
		t.Errorf("source_name %q, attendu vide", nom)
	}
	if corps := fiche(mux, cookie, recette.Id).Body.String(); strings.Contains(corps, "Exemple") {
		t.Errorf("la fiche affiche encore une source :\n%s", corps)
	}
}

// Critère 6 : une recette sans source reste le cas normal. Un champ vide ou
// rempli d'espaces enregistre une source vide, sans message d'erreur — et le
// schéma refuserait « ␣␣␣ » si l'adresse n'était pas rognée avant d'être
// posée.
func TestUneSourceVideOuDEspacesEstAcceptee(t *testing.T) {
	for nom, saisie := range map[string]string{"champ vide": "", "espaces": "   "} {
		t.Run(nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)

			champs := champsValides()
			champs.Set("source-url", saisie)
			rec := poste(t, mux, "/recettes", cookie, champs)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
			}
			if source := laRecette(t, app).GetString("source_url"); source != "" {
				t.Errorf("source_url %q, attendue vide", source)
			}
		})
	}
}

// Critère 7 : une adresse que le schéma refuse n'écrit rien, ni à la création
// ni à l'édition, et le refus se dit dans les mots du formulaire.
func TestUneSourceRefuseeParLeSchemaNEcritRien(t *testing.T) {
	const refusee = "javascript:alert(1)"

	t.Run("création", func(t *testing.T) {
		app, mux, cookie := carnetDeTest(t)

		champs := champsValides()
		champs.Set("source-url", refusee)
		rec := poste(t, mux, "/recettes", cookie, champs)

		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if n := len(recettes(t, app)); n != 0 {
			t.Errorf("%d recettes créées malgré le refus, 0 attendue", n)
		}
		if message := messageDErreur(t, rec.Body.String()); !strings.Contains(strings.ToLower(message), "source") {
			t.Errorf("message d'erreur %q, attendu nommant le champ « source »", message)
		}
	})

	t.Run("édition", func(t *testing.T) {
		app, mux, cookie := carnetDeTest(t)
		recette := recetteEnregistree(t, app, map[string]any{
			"title":      "Clafoutis",
			"source_url": adresseDeSource,
		})

		rec := poste(t, mux, "/recettes/"+recette.Id, cookie, champsAvecSource(refusee, ""))

		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		relue := relitLaRecette(t, app, recette.Id)
		if source := relue.GetString("source_url"); source != adresseDeSource {
			t.Errorf("source_url %q, attendue %q : la recette a été modifiée malgré le refus", source, adresseDeSource)
		}
		if titre := relue.GetString("title"); titre != "Clafoutis" {
			t.Errorf("titre %q, attendu « Clafoutis » : la recette a été modifiée malgré le refus", titre)
		}
	})
}

// Critère 8 : un formulaire refusé pour une autre raison revient avec ce que
// l'utilisateur avait tapé, source comprise — retaper l'adresse d'origine
// après une faute sur le titre serait une punition.
func TestUnFormulaireRefuseRendLAdresseDeSourceSaisie(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	champs := champsAvecSource(adresseDeSource, "Exemple")
	champs.Set("titre", "   ")
	rec := poste(t, mux, "/recettes", cookie, champs)

	corps := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if source := valeurDe(t, corps, "source-url"); source != adresseDeSource {
		t.Errorf("champ source-url %q, attendu %q", source, adresseDeSource)
	}
	if nom := valeurDe(t, corps, "source-nom"); nom != "Exemple" {
		t.Errorf("champ source-nom %q, attendu %q", nom, "Exemple")
	}
}

// --- Échappement et texte brut --------------------------------------------

// DOD.md §3 : une recette est du contenu étranger par nature, y compris quand
// c'est son propre auteur qui l'a tapée.
func TestLeTitreRessortEchappeDansLeFormulaireDEdition(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, map[string]any{"title": `<script>alert(1)</script>`})

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("balise script non échappée :\n%s", corps)
	}
	valeur := regexp.MustCompile(`name="titre"[^>]*value="([^"]*)"`).FindStringSubmatch(corps)
	if valeur == nil {
		t.Fatalf("attribut value du titre introuvable :\n%s", corps)
	}
	if !strings.Contains(valeur[1], "&lt;script&gt;") {
		t.Errorf("titre non échappé dans l'attribut : %q", valeur[1])
	}
}

// instructions est du texte brut : ce qui est saisi est stocké tel quel et
// ressort échappé. Le champ est un EditorField au schéma, mais rien n'y verse
// de HTML — l'éditeur riche et son assainissement sont une décision à part.
func TestInstructionsRestentDuTexteBrut(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	champs := champsValides()
	champs.Set("instructions", "Battre <b>vivement</b> & servir.")
	poste(t, mux, "/recettes", cookie, champs)

	recette := laRecette(t, app)
	if instructions := recette.GetString("instructions"); instructions != "Battre <b>vivement</b> & servir." {
		t.Errorf("instructions %q, attendues telles quelles", instructions)
	}

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()
	if strings.Contains(corps, "<b>vivement</b>") {
		t.Errorf("les instructions ressortent interprétées comme du HTML :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;b&gt;vivement&lt;/b&gt;") {
		t.Errorf("les instructions ne ressortent pas échappées :\n%s", corps)
	}
	if !strings.Contains(corps, "&amp; servir.") {
		t.Errorf("esperluette non échappée :\n%s", corps)
	}
}

// L'échappement d'un gabarit se contourne par un seul appel. Les deux portes
// se ferment ici, en une assertion qu'aucune relecture ne peut oublier : ni
// template.HTML dans le code, ni la fonction raw dans un gabarit.
func TestAucunGabaritNeContourneLEchappement(t *testing.T) {
	fichiers, err := vues.ReadDir("vues")
	if err != nil {
		t.Fatalf("liste des gabarits : %v", err)
	}

	appelRaw := regexp.MustCompile(`{{[^}]*\braw\b`)
	for _, fichier := range fichiers {
		contenu, err := vues.ReadFile("vues/" + fichier.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", fichier.Name(), err)
		}
		if appelRaw.Match(contenu) {
			t.Errorf("vues/%s emploie la fonction de gabarit raw", fichier.Name())
		}
	}

	if sources := chercheDansLesSources(t, "template.HTML"); len(sources) > 0 {
		t.Errorf("template.HTML employé dans %v", sources)
	}
}

// chercheDansLesSources rend les fichiers .go du paquet qui contiennent le
// motif donné.
func chercheDansLesSources(t *testing.T, motif string) []string {
	t.Helper()

	entrees, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("liste des sources : %v", err)
	}

	var trouves []string
	for _, entree := range entrees {
		// Les fichiers de test sont écartés : celui-ci nomme le motif
		// qu'il cherche, et se trouverait lui-même.
		if entree.IsDir() || !strings.HasSuffix(entree.Name(), ".go") || strings.HasSuffix(entree.Name(), "_test.go") {
			continue
		}
		contenu, err := os.ReadFile(entree.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", entree.Name(), err)
		}
		if bytes.Contains(contenu, []byte(motif)) {
			trouves = append(trouves, entree.Name())
		}
	}
	return trouves
}

// --- Recette introuvable ---------------------------------------------------

// Le contrôle de session passe avant la recherche de la recette : un visiteur
// ne doit pas apprendre, par un 404, quels identifiants existent.
func TestUnVisiteurSurUneRecetteInconnueVaALaConnexion(t *testing.T) {
	_, mux, _ := carnetDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/recettes/inexistante/modifier", nil)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if destination := rec.Header().Get("Location"); destination != "/connexion" {
		t.Errorf("Location %q, attendu %q", destination, "/connexion")
	}
}

// Un identifiant qui ne désigne rien est un 404, pas une erreur serveur — et
// une édition sur cet identifiant n'invente pas la recette manquante.
func TestUneRecetteInconnueEstUn404(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/recettes/inexistante/modifier", cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}

	rec = poste(t, mux, "/recettes/inexistante", cookie, champsValides())
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if n := len(recettes(t, app)); n != 0 {
		t.Errorf("%d recettes créées par une édition sur un identifiant inconnu, 0 attendue", n)
	}
}
