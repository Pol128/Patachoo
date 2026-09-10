package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// requete monte une route sans serveur ni base : un RequestEvent nu, une
// requête httptest et un enregistreur en guise de réponse. C'est le montage
// dont toutes les routes de rendu auront besoin.
func requete(t *testing.T, methode, cible string, entetes map[string]string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	t.Helper()

	req := httptest.NewRequest(methode, cible, nil)
	for nom, valeur := range entetes {
		req.Header.Set(nom, valeur)
	}
	rec := httptest.NewRecorder()

	e := &core.RequestEvent{}
	e.Request = req
	e.Response = rec

	return e, rec
}

// rendu exerce les deux chemins de rendre sur une page réelle, sans base ni
// session.
//
// La page de connexion, et non celle des recettes : ce qui est en jeu ici est
// la convention de rendu — mise en page, fragment, assets — et non le contenu.
// Une page qui exige une session obligerait chacun de ces tests à monter une
// base pour vérifier une balise <html>.
func rendu(t *testing.T, entetes map[string]string) (*httptest.ResponseRecorder, string) {
	t.Helper()

	e, rec := requete(t, http.MethodGet, "/connexion", entetes)
	// donneesConnexion, et non donneesPage : c'est ce que pageConnexion passe
	// au gabarit, et le corps y lit le réglage d'inscription. Un test qui
	// rendrait la page avec une autre structure ne vérifierait qu'un montage
	// qui n'existe nulle part.
	if err := rendre(e, "connexion.html", "connexion-corps.html", &donneesConnexion{
		donneesPage: donneesPage{Titre: "Connexion — Patachoo"},
	}); err != nil {
		t.Fatalf("rendre : %v", err)
	}

	return rec, rec.Body.String()
}

// Les gabarits et les assets partent dans le binaire : c'est go:embed qui le
// garantit, et seule une lecture depuis les variables embarquées le prouve.
func TestGabaritsEtAssetsSontEmbarques(t *testing.T) {
	cas := []struct {
		fsys    fs.FS
		chemin  string
		minimum int
	}{
		{vues, "vues/mise-en-page.html", 1},
		{vues, "vues/recettes.html", 1},
		{vues, "vues/recettes-resultats.html", 1},
		{vues, "vues/connexion.html", 1},
		{vues, "vues/connexion-corps.html", 1},
		{statique, "statique/htmx.min.js", 1},
		{statique, "statique/patachoo.css", 1},
	}

	for _, c := range cas {
		contenu, err := fs.ReadFile(c.fsys, c.chemin)
		if err != nil {
			t.Errorf("%s absent des fichiers embarqués : %v", c.chemin, err)
			continue
		}
		if len(contenu) < c.minimum {
			t.Errorf("%s embarqué mais vide", c.chemin)
		}
	}
}

func TestLeRenduProduitUnDocumentComplet(t *testing.T) {
	rec, corps := rendu(t, nil)

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q, attendu %q", ct, "text/html; charset=utf-8")
	}

	for _, attendu := range []string{"<!doctype html>", `<html lang="fr">`, "</html>"} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("document sans %q :\n%s", attendu, corps)
		}
	}

	titre := entreBalises(corps, "<title>", "</title>")
	if strings.TrimSpace(titre) == "" {
		t.Errorf("titre vide dans :\n%s", corps)
	}
}

// Pas de CDN : l'outil doit fonctionner sur un réseau coupé d'Internet. La
// formulation binaire, c'est l'absence de « :// » dans la page rendue.
func TestLeRenduNeSertQueDesAssetsLocaux(t *testing.T) {
	_, corps := rendu(t, nil)

	for _, attendu := range []string{"/statique/patachoo.css", "/statique/htmx.min.js"} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("document sans référence à %q :\n%s", attendu, corps)
		}
	}
	if strings.Contains(corps, "://") {
		t.Errorf("le document pointe vers un domaine tiers :\n%s", corps)
	}
}

func TestLaMiseEnPagePorteLaNavigation(t *testing.T) {
	_, corps := rendu(t, nil)

	for _, attendu := range []string{`href="/recettes"`, `href="/recettes/nouvelle"`} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("navigation sans %q :\n%s", attendu, corps)
		}
	}
}

// Une route qui répond à HTMX rend un fragment, pas la page entière : une page
// complète renvoyée dans un hx-target produit des pages imbriquées.
func TestLeRenduEstUnFragmentPourHTMX(t *testing.T) {
	_, corps := rendu(t, map[string]string{"HX-Request": "true"})

	for _, interdit := range []string{"<html", "<body"} {
		if strings.Contains(corps, interdit) {
			t.Errorf("fragment contenant %q :\n%s", interdit, corps)
		}
	}
	// Un fichier réduit à un {{define}}, chargé seul, rend une chaîne vide
	// sans erreur. Sans cette assertion, le piège passe inaperçu.
	if strings.TrimSpace(corps) == "" {
		t.Error("fragment vide : le gabarit chargé seul n'a rien rendu")
	}
}

func TestLeRenduEstUnDocumentCompletSansHTMX(t *testing.T) {
	_, corps := rendu(t, nil)

	for _, attendu := range []string{"<html", "<body"} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("document complet attendu, sans %q :\n%s", attendu, corps)
		}
	}
}

// DOD.md §3 : une recette importée est du contenu étranger par nature. Le test
// porte sur le HTML rendu par le gabarit, pas sur un appel d'échappement.
func TestLeGabaritEchappeSesEntrees(t *testing.T) {
	e, rec := requete(t, http.MethodGet, "/connexion", nil)

	err := rendre(e, "connexion.html", "connexion-corps.html", &donneesConnexion{
		donneesPage: donneesPage{
			Titre:   `<script>alert(1)</script>`,
			Message: `Chausson aux pommes & cannelle`,
		},
	})
	if err != nil {
		t.Fatalf("rendre : %v", err)
	}
	corps := rec.Body.String()

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("balise script non échappée :\n%s", corps)
	}
	titre := entreBalises(corps, "<title>", "</title>")
	if !strings.Contains(titre, "&lt;script&gt;") {
		t.Errorf("titre non échappé : %q", titre)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("titre non échappé dans le corps :\n%s", corps)
	}
	if !strings.Contains(corps, "pommes &amp; cannelle") {
		t.Errorf("esperluette non échappée :\n%s", corps)
	}
}

func TestAssetStatiqueEstServi(t *testing.T) {
	attendu, err := fs.ReadFile(statique, "statique/htmx.min.js")
	if err != nil {
		t.Fatalf("htmx.min.js non embarqué : %v", err)
	}

	e, rec := requete(t, http.MethodGet, "/statique/htmx.min.js", nil)
	e.Request.SetPathValue(apis.StaticWildcardParam, "htmx.min.js")

	if err := assetsStatiques()(e); err != nil {
		t.Fatalf("service de htmx.min.js : %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/javascript") {
		t.Errorf("Content-Type %q, attendu contenant %q", ct, "text/javascript")
	}
	if rec.Body.Len() != len(attendu) {
		t.Errorf("corps de %d octets, attendu %d", rec.Body.Len(), len(attendu))
	}
}

// Le false passé à apis.Static est délibéré : un asset absent doit être un 404,
// pas la page d'accueil déguisée en fichier JavaScript.
func TestAssetAbsentNestPasLaPageDAccueil(t *testing.T) {
	e, rec := requete(t, http.MethodGet, "/statique/absent.js", nil)
	e.Request.SetPathValue(apis.StaticWildcardParam, "absent.js")

	err := assetsStatiques()(e)

	if !errors.Is(err, router.ErrFileNotFound) {
		t.Errorf("erreur %v, attendu router.ErrFileNotFound", err)
	}
	// Le gestionnaire n'écrit rien lui-même : il remonte l'erreur, et c'est le
	// routeur qui la traduit. Lire rec.Code ne dirait que sa valeur par défaut.
	if statut := router.ToApiError(err).Status; statut != http.StatusNotFound {
		t.Errorf("statut %d, attendu %d", statut, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("la page d'accueil a été servie à la place d'un 404 :\n%s", rec.Body.String())
	}
}

// Le false n'est observable que si le système de fichiers porte un index.html
// à servir en repli : sur nos seuls assets, les deux valeurs se ressemblent.
// Ce test-là fait la différence, et c'est lui qui rougira le jour où quelqu'un
// passera l'indexFallback à true.
func TestAssetAbsentNeTombePasSurUnIndex(t *testing.T) {
	fsys := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>repli</html>")}}

	e, rec := requete(t, http.MethodGet, "/statique/absent.js", nil)
	e.Request.SetPathValue(apis.StaticWildcardParam, "absent.js")

	err := servirAssets(fsys)(e)

	if !errors.Is(err, router.ErrFileNotFound) {
		t.Errorf("erreur %v, attendu router.ErrFileNotFound", err)
	}
	if corps := rec.Body.String(); strings.Contains(corps, "repli") {
		t.Errorf("index.html servi en repli à la place d'un 404 : %q", corps)
	}
}

// entreBalises extrait ce qui sépare deux marqueurs, ou "" s'ils manquent.
func entreBalises(texte, ouvrante, fermante string) string {
	debut := strings.Index(texte, ouvrante)
	if debut < 0 {
		return ""
	}
	debut += len(ouvrante)

	fin := strings.Index(texte[debut:], fermante)
	if fin < 0 {
		return ""
	}

	return texte[debut : debut+fin]
}

// Toute classe posée dans un gabarit doit avoir une règle dans la feuille de
// style. Une classe sans règle ne se voit pas : la page s'affiche, sans erreur
// et sans le style annoncé, et l'écart ne se remarque qu'à l'œil — c'est-à-dire
// tard.
//
// La vérification est textuelle et volontairement bête : il ne s'agit pas de
// juger le style rendu, seulement de constater qu'une règle porte ce nom.
func TestChaqueClasseDesGabaritsAUneRegleDeStyle(t *testing.T) {
	feuille, err := statique.ReadFile("statique/patachoo.css")
	if err != nil {
		t.Fatalf("lecture de la feuille de style : %v", err)
	}

	fichiers, err := fs.Glob(vues, "vues/*.html")
	if err != nil {
		t.Fatalf("liste des gabarits : %v", err)
	}

	attributDeClasse := regexp.MustCompile(`class="([^"]*)"`)
	for _, fichier := range fichiers {
		gabarit, err := vues.ReadFile(fichier)
		if err != nil {
			t.Fatalf("lecture de %s : %v", fichier, err)
		}

		for _, attribut := range attributDeClasse.FindAllStringSubmatch(string(gabarit), -1) {
			for _, classe := range strings.Fields(attribut[1]) {
				// Le sélecteur est cherché entouré de ce qui peut le borner :
				// « .compte » ne doit pas se reconnaître dans « .compte-vide ».
				regle := regexp.MustCompile(`(^|[\s,}])\.` + regexp.QuoteMeta(classe) + `([\s,{:]|$)`)
				if !regle.MatchString(string(feuille)) {
					t.Errorf("%s pose la classe %q, que patachoo.css ne stylise pas", fichier, classe)
				}
			}
		}
	}
}
