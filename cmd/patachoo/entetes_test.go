package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// --- Referrer-Policy ------------------------------------------------------

// Une page de Patachoo fait partir des requêtes vers des sites tiers — l'aperçu
// de l'image chez le site importé, le lien du pied vers les versions publiées.
// Sans en-tête, l'adresse de l'instance part avec elles, au bon vouloir du
// défaut du navigateur. Le défaut d'un navigateur n'est pas une propriété du
// produit : c'est la réponse qui doit le dire.
//
// Le montage complet de serveurDeTest, et non un RequestEvent nu : ce qui est
// en jeu est qu'un middleware lié au routeur atteigne bien la réponse.
func TestLesReponsesPortentUneReferrerPolicyNoReferrer(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	for _, cas := range reponsesDeTest(mux, cookie, recette.Id) {
		if valeur := cas.rec.Header().Get("Referrer-Policy"); valeur != "no-referrer" {
			t.Errorf("%s : Referrer-Policy vaut %q, attendu %q", cas.nom, valeur, "no-referrer")
		}
	}
}

// Le middleware s'ajoute à celui de PocketBase, il ne le remplace pas : les
// trois en-têtes que pbSecurityHeaders pose restent sur la réponse. Leurs
// valeurs appartiennent à PocketBase et peuvent changer d'une version à
// l'autre ; leur présence, elle, nous concerne.
func TestLesReponsesGardentLesEntetesDeSecuriteDePocketBase(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	for _, cas := range reponsesDeTest(mux, cookie, recette.Id) {
		for _, entete := range []string{"X-Content-Type-Options", "X-Frame-Options", "X-XSS-Protection"} {
			if cas.rec.Header().Get(entete) == "" {
				t.Errorf("%s : %s absent des en-têtes de la réponse", cas.nom, entete)
			}
		}
	}
}

// reponsesDeTest joue les deux pages témoins : celle qu'un visiteur obtient
// sans compte, et celle d'où part l'aperçu de l'image distante.
func reponsesDeTest(mux http.Handler, cookie *http.Cookie, idRecette string) []struct {
	nom string
	rec *httptest.ResponseRecorder
} {
	return []struct {
		nom string
		rec *httptest.ResponseRecorder
	}{
		{"GET /connexion", avecCookie(mux, http.MethodGet, "/connexion", nil)},
		{"GET /recettes/{id}", fiche(mux, cookie, idRecette)},
	}
}

// --- Cache-Control --------------------------------------------------------

// TestEntetesDeCache monte le serveur complet — le même que les tests de
// session — et lit le Cache-Control de chaque famille de route.
//
// Un seul test pour les six cas, et des sous-tests plutôt que six fonctions :
// ce qu'ils vérifient est un unique comportement, « le middleware pose
// l'en-tête là et seulement là ». Retirer l'écriture de l'en-tête doit faire
// rougir ce test, et lui seul (DOD.md §2).
//
// Le montage complet, et non un RequestEvent nu : ce qui est en jeu est qu'un
// middleware lié au routeur atteigne la réponse, et qu'il ne l'atteigne pas là
// où il ne doit pas.
func TestEntetesDeCache(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	t.Run("une page authentifiée porte private, no-store", func(t *testing.T) {
		rec := demande(mux, "/recettes/"+recette.Id, cookie, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	t.Run("la page de connexion le porte aussi", func(t *testing.T) {
		rec := demande(mux, "/connexion", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	// Un fragment ne passe pas par rendreAvecStatut mais par rendLeBlocSeul :
	// c'est l'autre écriture de réponse du produit, et elle doit être couverte
	// par le même middleware.
	t.Run("un fragment htmx le porte", func(t *testing.T) {
		rec := demande(mux, "/tags/suggestions?tags=", cookie, map[string]string{"HX-Request": "true"})
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	// Une redirection n'écrit aucun corps : elle distingue un middleware lié au
	// routeur d'une écriture faite dans rendreAvecStatut, qu'elle n'atteint pas.
	t.Run("une redirection le porte", func(t *testing.T) {
		rec := demande(mux, "/recettes", nil, nil)
		if rec.Code != http.StatusFound {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusFound)
		}
		if destination := rec.Header().Get("Location"); destination != "/connexion" {
			t.Fatalf("Location %q, attendu %q", destination, "/connexion")
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "private, no-store" {
			t.Errorf("Cache-Control %q, attendu %q", pose, "private, no-store")
		}
	})

	// Les deux exclusions observables sur ce montage. no-store sur la feuille
	// de style remplacerait la mise en cache heuristique du navigateur par un
	// rechargement à chaque page ; sous /api/ vivent les vignettes, une par
	// recette de la liste.
	t.Run("un asset n'en porte aucun", func(t *testing.T) {
		rec := demande(mux, "/statique/patachoo.css", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "" {
			t.Errorf("Cache-Control %q, attendu aucun", pose)
		}
	})

	t.Run("une route de PocketBase n'en porte aucun", func(t *testing.T) {
		rec := demande(mux, "/api/health", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if pose := rec.Header().Get("Cache-Control"); pose != "" {
			t.Errorf("Cache-Control %q, attendu aucun", pose)
		}
	})
}

// TestPorteLeCacheControl couvre seule la décision de portée, qui est le vrai
// travail de la tâche : le préfixe plutôt que la route, pour qu'une page
// ajoutée demain soit couverte sans que personne n'y pense.
func TestPorteLeCacheControl(t *testing.T) {
	cas := []struct {
		chemin  string
		attendu bool
	}{
		{"/", true},
		{"/recettes/abc123", true},
		{"/connexion", true},
		{"/statique/patachoo.css", false},
		{"/api/files/recipes/x/y.jpg", false},
		{"/_/", false},
	}

	for _, c := range cas {
		t.Run(c.chemin, func(t *testing.T) {
			if porte := porteLeCacheControl(c.chemin); porte != c.attendu {
				t.Errorf("porteLeCacheControl(%q) = %v, attendu %v", c.chemin, porte, c.attendu)
			}
		})
	}
}

// --- Content-Security-Policy ----------------------------------------------

// La politique attendue, écrite ici en toutes lettres plutôt que reprise de la
// constante de production : un test qui lirait la même constante que le code
// passerait encore après qu'on l'a vidée. Les huit directives sont donc
// nommées deux fois dans le dépôt, et c'est voulu.
const politiqueAttendue = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' http: https:; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// Sur le montage complet, et non sur un RequestEvent nu : ce qui est en jeu est
// qu'un middleware lié au routeur atteigne bien la réponse.
func TestNosPagesPortentLaPolitiqueDeSecuriteDuContenu(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	// La page de connexion se demande en visiteur : un compte déjà connecté y
	// est renvoyé vers son carnet, et la réponse serait une redirection.
	cas := map[string]struct {
		cible  string
		cookie *http.Cookie
	}{
		"page de connexion": {"/connexion", nil},
		"fiche de recette":  {"/recettes/" + recette.Id, cookie},
	}
	for nom, cas := range cas {
		t.Run(nom, func(t *testing.T) {
			cible := cas.cible
			rec := avecCookie(mux, http.MethodGet, cible, cas.cookie)

			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d sur %s, attendu %d", rec.Code, cible, http.StatusOK)
			}
			if politique := rec.Header().Get("Content-Security-Policy"); politique != politiqueAttendue {
				t.Errorf("politique %q sur %s,\nattendue %q", politique, cible, politiqueAttendue)
			}
		})
	}
}

// La question « ce chemin reçoit-il notre politique ? » se vérifie ici et non à
// travers des routes montées : /_/ est enregistrée dans un hook OnServe que
// serveurDeTest ne déclenche pas, et n'existe donc pas en test. Sortie de la
// réponse, la règle reste vérifiable sur les chemins qui comptent.
func TestLaPolitiqueSApplique(t *testing.T) {
	cas := map[string]bool{
		"/connexion":                       true,
		"/recettes/abc":                    true,
		"/statique/patachoo.css":           true,
		"/_/":                              false,
		"/_/quelque-chose":                 false,
		"/api/files/recipes/abc/x.jpg":     false,
		"/api/collections/recipes/records": false,
	}
	for chemin, attendu := range cas {
		t.Run(chemin, func(t *testing.T) {
			if applique := laPolitiqueSApplique(chemin); applique != attendu {
				t.Errorf("laPolitiqueSApplique(%q) = %t, attendu %t", chemin, applique, attendu)
			}
		})
	}
}

// PocketBase pose ses propres politiques « seulement si l'en-tête est absent ».
// Un middleware racine s'exécute avant elles : poser la nôtre sur /api/files/
// remplacerait la politique sandbox des fichiers servis, ce qui est un recul.
func TestLesFichiersServisGardentLaPolitiqueDePocketBase(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"image": imageMinimale(t)})

	cible := "/api/files/recipes/" + recette.Id + "/" + recette.GetString("image")
	rec := avecCookie(mux, http.MethodGet, cible, cookie)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur %s, attendu %d", rec.Code, cible, http.StatusOK)
	}
	const sandbox = "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'; sandbox"
	if politique := rec.Header().Get("Content-Security-Policy"); politique != sandbox {
		t.Errorf("politique %q sur %s,\nattendue %q", politique, cible, sandbox)
	}
}

// Deux middlewares qui poseraient la politique, c'est celui qui passe en
// dernier qui décide — et personne ne saurait lequel. L'en-tête ne s'écrit donc
// qu'à un seul endroit du code de production.
func TestLEnTeteNEstEcritQuUneFois(t *testing.T) {
	entrees, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("liste des sources : %v", err)
	}

	// La forme entre guillemets, et non le nom nu : un commentaire qui nomme
	// l'en-tête n'est pas une écriture.
	const ecriture = `"Content-Security-Policy"`

	total := 0
	var fichiers []string
	for _, entree := range entrees {
		if entree.IsDir() || !strings.HasSuffix(entree.Name(), ".go") || strings.HasSuffix(entree.Name(), "_test.go") {
			continue
		}
		contenu, err := os.ReadFile(entree.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", entree.Name(), err)
		}
		if n := strings.Count(string(contenu), ecriture); n > 0 {
			total += n
			fichiers = append(fichiers, entree.Name())
		}
	}

	if total != 1 {
		t.Errorf("%d écritures de %s dans %v, une seule attendue", total, ecriture, fichiers)
	}
}

// --- htmx, réglé pour tenir sous la politique ------------------------------

// Sans includeIndicatorStyles:false, htmx injecte un <style> en ligne au
// chargement et style-src 'self' le bloque. allowEval et allowScriptTags
// ferment au passage les deux chemins d'exécution de htmx, qu'aucun gabarit
// n'emploie. La meta doit précéder le <script src> : htmx la lit au chargement.
func TestLaMetaHtmxConfigFermeCeQueLaPolitiqueInterdit(t *testing.T) {
	_, mux := serveurDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/connexion", nil).Body.String()

	tete := entreLesBalises(t, corps, "<head>", "</head>")
	for _, reglage := range []string{
		`name="htmx-config"`,
		`"includeIndicatorStyles":false`,
		`"allowEval":false`,
		`"allowScriptTags":false`,
	} {
		if !strings.Contains(tete, reglage) {
			t.Errorf("le <head> ne porte pas %s :\n%s", reglage, tete)
		}
	}

	meta := strings.Index(tete, `name="htmx-config"`)
	script := strings.Index(tete, "<script")
	if meta < 0 || script < 0 || meta > script {
		t.Errorf("la meta htmx-config ne précède pas le <script> :\n%s", tete)
	}
}

// entreLesBalises rend ce que le corps porte entre deux balises, ou fait
// échouer le test s'il n'en porte pas.
func entreLesBalises(t *testing.T, corps, ouvrante, fermante string) string {
	t.Helper()

	debut := strings.Index(corps, ouvrante)
	fin := strings.Index(corps, fermante)
	if debut < 0 || fin < debut {
		t.Fatalf("ni %s ni %s dans la réponse :\n%s", ouvrante, fermante, corps)
	}
	return corps[debut+len(ouvrante) : fin]
}

// --- Ce que les gabarits n'ont plus le droit d'introduire -------------------

// La politique est stricte ; un gabarit qui poserait un style ou un
// gestionnaire en ligne ne serait pas refusé à l'écriture, il cesserait
// simplement de s'afficher ou de réagir, en production et sans un mot. Ce test
// le dit à la place du navigateur. Il passe sur les vingt-sept gabarits
// d'aujourd'hui : aucun n'en porte.
func TestAucunGabaritNIntroduitCeQueLaPolitiqueBloque(t *testing.T) {
	interdits := map[string]*regexp.Regexp{
		"un attribut style en ligne":   regexp.MustCompile(`(?i)\sstyle\s*=`),
		"une balise <style>":           regexp.MustCompile(`(?i)<style[\s>]`),
		"un gestionnaire on… en ligne": regexp.MustCompile(`(?i)\son[a-z]+\s*=`),
		"un attribut hx-on":            regexp.MustCompile(`(?i)hx-on`),
		`un hx-vals="js:"`:             regexp.MustCompile(`(?i)hx-vals\s*=\s*["']?\s*js:`),
	}

	fichiers, err := vues.ReadDir("vues")
	if err != nil {
		t.Fatalf("liste des gabarits : %v", err)
	}

	for _, fichier := range fichiers {
		contenu, err := vues.ReadFile("vues/" + fichier.Name())
		if err != nil {
			t.Fatalf("lecture de %s : %v", fichier.Name(), err)
		}
		for quoi, motif := range interdits {
			if trouve := motif.Find(contenu); trouve != nil {
				t.Errorf("vues/%s introduit %s : %q", fichier.Name(), quoi, trouve)
			}
		}
	}
}
