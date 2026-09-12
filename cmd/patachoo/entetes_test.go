package main

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

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

// --- La politique sur nos pages --------------------------------------------

// Sur le montage complet, et non sur un RequestEvent nu : ce qui est en jeu est
// qu'un middleware lié au routeur atteigne bien la réponse.
func TestNosPagesPortentLaPolitiqueDeSecuriteDuContenu(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, nil)

	cas := map[string]string{
		"page de connexion": "/connexion",
		"fiche de recette":  "/recettes/" + recette.Id,
	}
	for nom, cible := range cas {
		t.Run(nom, func(t *testing.T) {
			rec := avecCookie(mux, http.MethodGet, cible, cookie)

			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d sur %s, attendu %d", rec.Code, cible, http.StatusOK)
			}
			if politique := rec.Header().Get("Content-Security-Policy"); politique != politiqueAttendue {
				t.Errorf("politique %q sur %s,\nattendue %q", politique, cible, politiqueAttendue)
			}
		})
	}
}

// --- La décision de portée -------------------------------------------------

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

// --- Ce que notre politique ne doit pas écraser ----------------------------

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

// Notre middleware s'ajoute à securityHeaders() de PocketBase, il ne le
// remplace pas : les trois en-têtes dont nous héritons restent sur nos pages.
func TestLesEntetesDePocketBaseResistentAuNotre(t *testing.T) {
	_, mux := serveurDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/connexion", nil)

	herites := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "SAMEORIGIN",
		"X-XSS-Protection":       "1; mode=block",
	}
	for nom, attendu := range herites {
		if valeur := rec.Header().Get(nom); valeur != attendu {
			t.Errorf("%s vaut %q, attendu %q", nom, valeur, attendu)
		}
	}
}

// --- Une seule écriture de l'en-tête ---------------------------------------

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
