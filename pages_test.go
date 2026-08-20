package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
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

// accueil rend la page d'accueil et renvoie le corps de la réponse.
func accueil(t *testing.T, entetes map[string]string) (*httptest.ResponseRecorder, string) {
	t.Helper()

	e, rec := requete(t, http.MethodGet, "/", entetes)
	if err := pageAccueil(e); err != nil {
		t.Fatalf("pageAccueil : %v", err)
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
		{vues, "vues/accueil.html", 1},
		{vues, "vues/accueil-corps.html", 1},
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

func TestAccueilRendUnDocumentComplet(t *testing.T) {
	rec, corps := accueil(t, nil)

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
func TestAccueilNeSertQueDesAssetsLocaux(t *testing.T) {
	_, corps := accueil(t, nil)

	for _, attendu := range []string{"/statique/patachoo.css", "/statique/htmx.min.js"} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("document sans référence à %q :\n%s", attendu, corps)
		}
	}
	if strings.Contains(corps, "://") {
		t.Errorf("le document pointe vers un domaine tiers :\n%s", corps)
	}
}

func TestAccueilPorteLaNavigation(t *testing.T) {
	_, corps := accueil(t, nil)

	for _, attendu := range []string{`href="/recettes"`, `href="/recettes/nouvelle"`} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("navigation sans %q :\n%s", attendu, corps)
		}
	}
}

// Une route qui répond à HTMX rend un fragment, pas la page entière : une page
// complète renvoyée dans un hx-target produit des pages imbriquées.
func TestAccueilRendUnFragmentAHTMX(t *testing.T) {
	_, corps := accueil(t, map[string]string{"HX-Request": "true"})

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

func TestAccueilRendLeDocumentCompletSansHTMX(t *testing.T) {
	_, corps := accueil(t, nil)

	for _, attendu := range []string{"<html", "<body"} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("document complet attendu, sans %q :\n%s", attendu, corps)
		}
	}
}

// DOD.md §3 : une recette importée est du contenu étranger par nature. Le test
// porte sur le HTML rendu par le gabarit, pas sur un appel d'échappement.
func TestLeGabaritEchappeSesEntrees(t *testing.T) {
	e, rec := requete(t, http.MethodGet, "/", nil)

	err := rendre(e, "accueil.html", "accueil-corps.html", donneesPage{
		Titre:   `<script>alert(1)</script>`,
		Message: `Chausson aux pommes & cannelle`,
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
