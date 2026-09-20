package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Pol128/Patachoo/recuperation"
	"github.com/pocketbase/pocketbase/core"
)

// --- Montage ---------------------------------------------------------------

// urlSource est l'adresse que les tests collent dans le champ. Elle ne
// désigne rien : aucun test de ce fichier ne touche le réseau.
const urlSource = "https://fourneaux-de-perlimpinpin.example/recettes/gratin"

// avecRecuperateur remplace, le temps du test, ce qui va chercher la page
// distante.
//
// C'est le seul point d'injection, et il est au-dessus de PATA-8 : cette
// tâche-là a ses propres tests — SSRF, délai, taille, redirections — et les
// refaire ici les ferait diverger. Ce qui se vérifie de ce côté de la
// frontière, c'est ce que l'import fait de ce que PATA-8 lui rend.
func avecRecuperateur(t *testing.T, aller func(context.Context, string, ...recuperation.Option) (recuperation.Page, error)) {
	t.Helper()

	precedent := recuperePage
	recuperePage = aller
	t.Cleanup(func() { recuperePage = precedent })
}

// reseauPiege fait échouer le test si une requête sortante part. C'est ce qui
// distingue « refusé » de « refusé après coup » : une URL invalide, ou un
// visiteur sans session, ne doivent déclencher aucun appel.
func reseauPiege(t *testing.T) {
	t.Helper()

	avecRecuperateur(t, func(_ context.Context, adresse string, _ ...recuperation.Option) (recuperation.Page, error) {
		t.Errorf("une requête sortante est partie vers %q", adresse)
		return recuperation.Page{}, errors.New("le réseau ne devait pas être touché")
	})
}

// sert fait rendre cette page-là, quelle que soit l'URL demandée.
func sert(t *testing.T, corps []byte, urlFinale string) {
	t.Helper()

	avecRecuperateur(t, func(_ context.Context, _ string, _ ...recuperation.Option) (recuperation.Page, error) {
		return recuperation.Page{Corps: corps, URLFinale: urlFinale, TypeContenu: "text/html"}, nil
	})
}

// echoue fait rendre l'échec nommé que PATA-8 rendrait.
func echoue(t *testing.T, err error) {
	t.Helper()

	avecRecuperateur(t, func(_ context.Context, _ string, _ ...recuperation.Option) (recuperation.Page, error) {
		return recuperation.Page{}, err
	})
}

// pageDuCorpus lit une page du corpus de PATA-28. Les entrées de l'import sont
// celles-là : un second corpus finirait par diverger du premier.
//
// Le chemin remonte à la racine du module : go test place le répertoire
// courant sur le paquet testé, et le corpus vit chez jsonld/, deux crans plus
// haut depuis cmd/patachoo/.
func pageDuCorpus(t *testing.T, nom string) []byte {
	t.Helper()

	corps, err := os.ReadFile(filepath.Join("..", "..", "jsonld", "testdata", nom+".html"))
	if err != nil {
		t.Fatalf("lecture du cas %q : %v", nom, err)
	}
	return corps
}

// pageAvecRecette fabrique une page portant une entité Recipe. Le corpus
// couvre les formes réellement observées ; ces pages-ci couvrent ce qu'il ne
// porte pas — un rendement sans chiffre, un og:site_name, un titre hostile.
func pageAvecRecette(entete, recette string) []byte {
	return []byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8">` + entete +
		`<script type="application/ld+json">` + recette + `</script>` +
		`</head><body></body></html>`)
}

// importeLURL poste une adresse sur la route d'import.
func importeLURL(mux http.Handler, cookie *http.Cookie, adresse string, entetes map[string]string) *httptest.ResponseRecorder {
	corps := leJetonEstPose(url.Values{"url": {adresse}}).Encode()
	req := httptest.NewRequest(http.MethodPost, "/recettes/importer", strings.NewReader(corps))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieDuJetonDeTest())
	for nom, valeur := range entetes {
		req.Header.Set(nom, valeur)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// corpsImporte joue l'import de urlSource et rend le document, en exigeant 200.
func corpsImporte(t *testing.T, mux http.Handler, cookie *http.Cookie) string {
	t.Helper()

	rec := importeLURL(mux, cookie, urlSource, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// --- Lecture du formulaire rendu -------------------------------------------

// valeurDe lit l'attribut value d'un champ que le formulaire porte toujours.
func valeurDe(t *testing.T, corps, champ string) string {
	t.Helper()

	trouve := champRendu(champ).FindStringSubmatch(corps)
	if trouve == nil {
		t.Fatalf("champ %q introuvable dans la réponse :\n%s", champ, corps)
	}
	return trouve[1]
}

// valeurEventuelleDe rend "" quand le champ n'est pas rendu du tout : les
// champs cachés de la source ne s'écrivent que lorsqu'ils portent quelque
// chose.
func valeurEventuelleDe(corps, champ string) string {
	trouve := champRendu(champ).FindStringSubmatch(corps)
	if trouve == nil {
		return ""
	}
	return trouve[1]
}

func champRendu(champ string) *regexp.Regexp {
	return regexp.MustCompile(`name="` + regexp.QuoteMeta(champ) + `"[^>]*value="([^"]*)"`)
}

// texteDe lit le contenu d'un <textarea>, entités décodées : c'est le texte
// que l'utilisateur relit, pas sa forme échappée.
func texteDe(t *testing.T, corps, champ string) string {
	t.Helper()

	motif := regexp.MustCompile(`(?s)<textarea[^>]*name="` + regexp.QuoteMeta(champ) + `"[^>]*>(.*?)</textarea>`)
	trouve := motif.FindStringSubmatch(corps)
	if trouve == nil {
		t.Fatalf("textarea %q introuvable dans la réponse :\n%s", champ, corps)
	}
	return html.UnescapeString(trouve[1])
}

// lignesDuChamp découpe un textarea en lignes non vides, dans l'ordre.
func lignesDuChamp(t *testing.T, corps, champ string) []string {
	t.Helper()

	lignes := []string{}
	for _, ligne := range strings.Split(texteDe(t, corps, champ), "\n") {
		if rognee := strings.TrimSpace(ligne); rognee != "" {
			lignes = append(lignes, rognee)
		}
	}
	return lignes
}

// estLeFormulaireDeRecette dit si c'est la fiche pré-remplie qui a été rendue,
// et non le champ « coller l'URL ».
func estLeFormulaireDeRecette(corps string) bool {
	return strings.Contains(corps, `name="ingredients"`)
}

// --- Le parcours nominal ---------------------------------------------------

// Critère 2 : le titre extrait et une ligne par ingrédient, dans l'ordre de la
// source.
func TestImportPreRemplitLeTitreEtLesIngredients(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, pageDuCorpus(t, "graphe-imbrique"), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if titre := valeurDe(t, corps, "titre"); titre != "Gratin de courge à la sauge" {
		t.Errorf("titre %q, attendu le titre extrait", titre)
	}
	attendus := []string{
		"800 g de courge butternut",
		"12 feuilles de sauge",
		"20 cl de crème entière",
		"60 g de parmesan râpé",
	}
	if lignes := lignesDuChamp(t, corps, "ingredients"); !slices.Equal(lignes, attendus) {
		t.Errorf("ingrédients %q, attendus %q", lignes, attendus)
	}
}

// Les étapes deviennent les instructions, une par ligne et dans l'ordre.
func TestImportPreRemplitLesInstructionsUneEtapeParLigne(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, pageDuCorpus(t, "graphe-imbrique"), urlSource)

	corps := corpsImporte(t, mux, cookie)

	attendues := []string{
		"Peler la courge et la couper en cubes.",
		"Faire revenir la sauge dans le beurre.",
		"Enfourner 45 minutes à 180 °C.",
	}
	if lignes := lignesDuChamp(t, corps, "instructions"); !slices.Equal(lignes, attendues) {
		t.Errorf("instructions %q, attendues %q", lignes, attendues)
	}
}

// Critère 3 : les durées sont déjà en minutes, le rendement se réduit à son
// premier entier — et un rendement sans chiffre laisse le champ vide, pas à 0.
func TestImportPreRemplitLesDureesEtLesPortions(t *testing.T) {
	sansChiffre := pageAvecRecette("", `{"@context":"https://schema.org","@type":"Recipe",
		"name":"Confiture de fruits imaginaires","recipeYield":"un grand bocal"}`)

	cas := []struct {
		nom                            string
		page                           []byte
		portions, preparation, cuisson string
	}{
		{"durées ISO 8601", pageDuCorpus(t, "durees-iso8601"), "", "25", "210"},
		{"rendement en texte", pageDuCorpus(t, "rendement-texte"), "4", "", ""},
		{"rendement en tableau", pageDuCorpus(t, "rendement-tableau"), "6", "", ""},
		{"rendement sans chiffre", sansChiffre, "", "", ""},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			sert(t, c.page, urlSource)

			corps := corpsImporte(t, mux, cookie)

			if portions := valeurDe(t, corps, "portions"); portions != c.portions {
				t.Errorf("portions %q, attendu %q", portions, c.portions)
			}
			if preparation := valeurDe(t, corps, "temps-preparation"); preparation != c.preparation {
				t.Errorf("temps de préparation %q, attendu %q", preparation, c.preparation)
			}
			if cuisson := valeurDe(t, corps, "temps-cuisson"); cuisson != c.cuisson {
				t.Errorf("temps de cuisson %q, attendu %q", cuisson, c.cuisson)
			}
		})
	}
}

// Déroulé n° 5 : l'image extraite est montrée à distance dans l'aperçu, pas
// attachée — le téléchargement est PATA-10. Les deux formes que le corpus
// porte, l'ImageObject et le tableau, se lisent pareil à l'arrivée.
func TestImportPreRemplitLImageDistante(t *testing.T) {
	cas := []struct {
		cas     string
		attendu string
	}{
		{"image-objet", "https://fourneaux-de-perlimpinpin.example/images/veloute-panais.jpg"},
		{"image-tableau", "https://fourneaux-de-perlimpinpin.example/images/clafoutis-16x9.jpg"},
	}

	for _, c := range cas {
		t.Run(c.cas, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			sert(t, pageDuCorpus(t, c.cas), urlSource)

			corps := corpsImporte(t, mux, cookie)

			if !strings.Contains(corps, `src="`+c.attendu+`"`) {
				t.Errorf("l'image extraite n'est pas affichée dans l'aperçu :\n%s", corps)
			}
		})
	}
}

// Critère 4 : source_url est l'URL finale suivie, pas celle qui a été collée.
func TestImportPorteLURLFinaleSuivie(t *testing.T) {
	const finale = "https://apres-redirection.example/vrai-lien"

	_, mux, cookie := carnetDeTest(t)
	sert(t, pageDuCorpus(t, "graphe-imbrique"), finale)

	corps := corpsImporte(t, mux, cookie)

	if source := valeurEventuelleDe(corps, "source-url"); source != finale {
		t.Errorf("source_url %q, attendue l'URL finale %q", source, finale)
	}
}

// Critère 4 : source_name vaut og:site_name quand il existe, le nom d'hôte
// sinon.
func TestImportPorteLeNomDuSite(t *testing.T) {
	cas := []struct {
		nom     string
		entete  string
		attendu string
	}{
		{"og:site_name présent", `<meta property="og:site_name" content="Les Fourneaux de Perlimpinpin">`, "Les Fourneaux de Perlimpinpin"},
		{"og:site_name absent", "", "fourneaux-de-perlimpinpin.example"},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			sert(t, pageAvecRecette(c.entete, `{"@type":"Recipe","name":"Gratin"}`), urlSource)

			corps := corpsImporte(t, mux, cookie)

			if site := valeurEventuelleDe(corps, "source-nom"); site != c.attendu {
				t.Errorf("source_name %q, attendu %q", site, c.attendu)
			}
		})
	}
}

// Le formulaire pré-rempli poste sur la route d'enregistrement de PATA-15 :
// c'est la charnière du parcours, « l'utilisateur valide ou corrige → la
// recette est enregistrée ». Les deux parcours rendent ce formulaire, les deux
// doivent le poster au bon endroit.
func TestLeFormulairePreRempliPosteVersLEnregistrement(t *testing.T) {
	cas := []struct {
		nom  string
		page []byte
	}{
		{"parcours nominal", pageAvecRecette("", `{"@type":"Recipe","name":"Gratin"}`)},
		{"parcours d'échec", []byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8">` +
			`<title>Tarte aux poireaux imaginaire</title></head><body></body></html>`)},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			sert(t, c.page, urlSource)

			corps := corpsImporte(t, mux, cookie)

			if !estLeFormulaireDeRecette(corps) {
				t.Fatalf("le formulaire de recette n'a pas été rendu :\n%s", corps)
			}
			if !strings.Contains(corps, `action="/recettes"`) {
				t.Errorf("le formulaire pré-rempli ne poste pas sur /recettes :\n%s", corps)
			}
		})
	}
}

// Critère 10 : la description extraite ne va nulle part — la collection n'a
// pas de champ pour elle, et on ne la mélange pas aux instructions.
func TestImportNAffichePasLaDescription(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, pageDuCorpus(t, "graphe-imbrique"), urlSource)

	corps := corpsImporte(t, mux, cookie)

	const description = "Un gratin de courge fondant, relevé de sauge fraîche."
	if strings.Contains(corps, description) {
		t.Errorf("la description extraite apparaît dans le formulaire :\n%s", corps)
	}
}

// --- Le parcours d'échec ---------------------------------------------------

// cause décrit un échec du déroulé n° 6 et la manière de le provoquer.
type cause struct {
	nom     string
	prepare func(*testing.T)
}

// lesCauses énumère les sept causes que la tâche distingue, plus la page trop
// volumineuse que PATA-8 sait aussi rendre. Chacune doit avoir son propre
// message : elles n'appellent pas la même réaction de l'utilisateur.
func lesCauses() []cause {
	return []cause{
		{"site injoignable", func(t *testing.T) {
			echoue(t, &recuperation.Erreur{Cause: recuperation.Injoignable, URL: urlSource})
		}},
		{"refus du site", func(t *testing.T) {
			echoue(t, &recuperation.Erreur{Cause: recuperation.RefusHTTP, Code: http.StatusForbidden, URL: urlSource})
		}},
		{"refusée par la politique", func(t *testing.T) {
			echoue(t, &recuperation.Erreur{Cause: recuperation.RefuseeParPolitique, URL: urlSource})
		}},
		{"page trop volumineuse", func(t *testing.T) {
			echoue(t, &recuperation.Erreur{Cause: recuperation.TailleMax, URL: urlSource})
		}},
		{"attente du tour abandonnée", func(t *testing.T) {
			echoue(t, &recuperation.Erreur{Cause: recuperation.AttenteDeCadence, URL: urlSource})
		}},
		{"JSON-LD sans recette", func(t *testing.T) {
			sert(t, pageDuCorpus(t, "ldjson-sans-recette"), urlSource)
		}},
		{"aucun balisage", func(t *testing.T) {
			sert(t, pageDuCorpus(t, "aucun-balisage"), urlSource)
		}},
		{"bloc ld+json illisible", func(t *testing.T) {
			sert(t, pageDuCorpus(t, "json-malforme"), urlSource)
		}},
		{"recette sans titre", func(t *testing.T) {
			sert(t, pageDuCorpus(t, "titre-absent"), urlSource)
		}},
	}
}

// Critère 5 : chaque cause a son message, aucune n'en partage un avec une
// autre, et aucun ne dit « une erreur est survenue ».
func TestChaqueCauseRendUnMessageQuiLuiEstPropre(t *testing.T) {
	messages := map[string]string{}

	for _, c := range lesCauses() {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			c.prepare(t)

			corps := corpsImporte(t, mux, cookie)

			message := messageDErreur(t, corps)
			if message == "" {
				t.Fatalf("aucun message rendu pour %q :\n%s", c.nom, corps)
			}
			if strings.Contains(strings.ToLower(message), "une erreur est survenue") {
				t.Errorf("message creux pour %q : %q", c.nom, message)
			}
			if autre, deja := messages[message]; deja {
				t.Errorf("%q et %q rendent le même message : %q", c.nom, autre, message)
			}
			messages[message] = c.nom
		})
	}
}

// Les causes que le déroulé regroupe partagent leur message : un délai dépassé
// est une manière de ne pas joindre le site, un robots.txt qui refuse est une
// manière de ne pas avoir le droit d'y aller.
func TestLesCausesRegroupeesPartagentLeurMessage(t *testing.T) {
	cas := []struct{ groupee, avec string }{
		{recuperation.DelaiDepasse, recuperation.Injoignable},
		{recuperation.RobotsInterdit, recuperation.RefuseeParPolitique},
	}

	for _, c := range cas {
		t.Run(c.groupee, func(t *testing.T) {
			groupee := messageDeLEchec(&recuperation.Erreur{Cause: c.groupee, URL: urlSource})
			avec := messageDeLEchec(&recuperation.Erreur{Cause: c.avec, URL: urlSource})

			if groupee != avec {
				t.Errorf("%q rend %q, attendu le même message que %q : %q", c.groupee, groupee, c.avec, avec)
			}
		})
	}
}

// Critère 6 : l'extraction échoue, mais Open Graph donne souvent le titre et
// l'image. Le formulaire est rendu avec ce qui a pu être trouvé.
func TestImportEchoueRendLeFormulairePreRempliParOpenGraph(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, pageDuCorpus(t, "aucun-balisage"), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if !estLeFormulaireDeRecette(corps) {
		t.Fatalf("le formulaire de recette n'a pas été rendu :\n%s", corps)
	}
	if titre := valeurDe(t, corps, "titre"); titre != "Ma tambouille du mardi soir" {
		t.Errorf("titre %q, attendu celui d'og:title", titre)
	}
	const image = "https://fourneaux-de-perlimpinpin.example/images/tambouille.jpg"
	if !strings.Contains(corps, image) {
		t.Errorf("l'image d'og:image n'est pas affichée :\n%s", corps)
	}
	if source := valeurEventuelleDe(corps, "source-url"); source != urlSource {
		t.Errorf("source_url %q, attendue %q", source, urlSource)
	}
}

// À défaut d'og:title, la balise <title> du document.
func TestImportEchoueRetombeSurLaBaliseTitle(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, []byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8">`+
		`<title>Tarte aux poireaux imaginaire</title></head><body></body></html>`), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if titre := valeurDe(t, corps, "titre"); titre != "Tarte aux poireaux imaginaire" {
		t.Errorf("titre %q, attendu celui de la balise title", titre)
	}
}

// Critère 6, seconde moitié : sur une page sans rien d'exploitable, le
// formulaire est rendu quand même, source_url renseignée et le reste vide.
func TestImportEchoueRendUnFormulaireVideMaisSource(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, []byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8">`+
		`</head><body><p>Rien à en tirer.</p></body></html>`), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if !estLeFormulaireDeRecette(corps) {
		t.Fatalf("le formulaire de recette n'a pas été rendu :\n%s", corps)
	}
	if source := valeurEventuelleDe(corps, "source-url"); source != urlSource {
		t.Errorf("source_url %q, attendue %q", source, urlSource)
	}
	for _, champ := range []string{"titre", "portions", "temps-preparation", "temps-cuisson"} {
		if valeur := valeurDe(t, corps, champ); valeur != "" {
			t.Errorf("champ %q rempli de %q, attendu vide", champ, valeur)
		}
	}
	if lignes := lignesDuChamp(t, corps, "ingredients"); len(lignes) != 0 {
		t.Errorf("ingrédients %q, attendus vides", lignes)
	}
}

// Le site injoignable n'a rendu aucune page : le formulaire est rendu tout de
// même, avec la seule adresse que l'utilisateur a collée.
func TestImportRendLeFormulaireQuandLaPageNaPasEteAtteinte(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	echoue(t, &recuperation.Erreur{Cause: recuperation.Injoignable, URL: urlSource})

	corps := corpsImporte(t, mux, cookie)

	if !estLeFormulaireDeRecette(corps) {
		t.Fatalf("le formulaire de recette n'a pas été rendu :\n%s", corps)
	}
	if source := valeurEventuelleDe(corps, "source-url"); source != urlSource {
		t.Errorf("source_url %q, attendue l'adresse collée %q", source, urlSource)
	}
}

// Critère 11 : l'erreur technique ne fuit pas dans la page.
func TestImportNeFuitPasLErreurTechnique(t *testing.T) {
	const marqueur = "zzmarqueurtechniquezz"

	cas := []struct {
		nom string
		err error
	}{
		{"cause nommée portant une URL technique", &recuperation.Erreur{
			Cause: recuperation.Injoignable,
			URL:   "https://" + marqueur + ".invalide/interne",
		}},
		{"erreur quelconque", fmt.Errorf("dial tcp 203.0.113.7:443: %s", marqueur)},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			echoue(t, c.err)

			corps := corpsImporte(t, mux, cookie)

			if strings.Contains(corps, marqueur) {
				t.Errorf("le détail technique fuit dans la page :\n%s", corps)
			}
			if messageDErreur(t, corps) == "" {
				t.Errorf("aucun message rendu :\n%s", corps)
			}
		})
	}
}

// --- Sécurité --------------------------------------------------------------

// Critère 12, DOD.md §3 : une recette importée est du contenu étranger par
// nature. Le titre publié par le site ressort échappé.
func TestImportEchappeLeTitreDuSite(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	// « <\/script> » est l'échappement JSON par lequel un site publie une
	// balise fermante sans clore son propre bloc — la valeur extraite, elle,
	// vaut bien <script>alert(1)</script>.
	sert(t, pageAvecRecette("", `{"@type":"Recipe","name":"<script>alert(1)<\/script>"}`), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("balise script non échappée :\n%s", corps)
	}
	if titre := valeurDe(t, corps, "titre"); !strings.Contains(titre, "&lt;script&gt;") {
		t.Errorf("titre non échappé dans l'attribut : %q", titre)
	}
}

// L'image aussi vient du site : une adresse en javascript: ne doit pas
// atterrir telle quelle dans un attribut src.
//
// La page ne porte pas de Recipe : c'est le chemin d'échec qui lit og:image,
// et une page balisée n'y passerait jamais. Le test exige donc d'abord que
// l'image soit rendue, faute de quoi il vérifierait l'absence d'une URL dans
// une page sans balise <img — c'est-à-dire rien.
func TestImportNeRendPasUneImageEnSchemaExecutable(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	sert(t, []byte(`<!doctype html><html lang="fr"><head><meta charset="utf-8">`+
		`<meta property="og:title" content="Gratin">`+
		`<meta property="og:image" content="javascript:alert(1)">`+
		`</head><body></body></html>`), urlSource)

	corps := corpsImporte(t, mux, cookie)

	if !strings.Contains(corps, "<img") {
		t.Fatalf("aucune image rendue : le test ne vérifierait rien\n%s", corps)
	}
	if strings.Contains(corps, "javascript:alert(1)") {
		t.Errorf("une URL exécutable est rendue telle quelle :\n%s", corps)
	}
}

// Critère 8 : une URL vide, relative ou d'un schéma que nous ne suivons pas est
// refusée sans qu'aucune requête ne parte.
func TestImportRefuseUneURLInexploitableSansToucherAuReseau(t *testing.T) {
	for _, c := range lesRefusDeSaisie() {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			reseauPiege(t)

			rec := importeLURL(mux, cookie, c.adresse, nil)

			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			corps := rec.Body.String()
			if estLeFormulaireDeRecette(corps) {
				t.Errorf("le formulaire de recette a été rendu pour une URL refusée :\n%s", corps)
			}
			if !strings.Contains(corps, `name="url"`) {
				t.Errorf("le champ « coller l'URL » n'a pas été re-rendu :\n%s", corps)
			}
			if messageDErreur(t, corps) == "" {
				t.Errorf("aucun message rendu :\n%s", corps)
			}
			// Le champ se rend avec ce qui a été tapé : une saisie refusée ne
			// doit pas se retaper. Sans cette exigence, donneesImport.URL —
			// qui n'existe que pour ça — se vide sans faire rougir personne.
			if attendue := strings.TrimSpace(c.adresse); attendue != "" {
				if rendue := valeurDe(t, corps, "url"); rendue != attendue {
					t.Errorf("le champ re-rendu porte %q, attendu l'adresse soumise %q", rendue, attendue)
				}
			}
		})
	}
}

// lesRefusDeSaisie : les saisies que refusDeLAdresse écarte, chacune avec la
// cause qui la fait écarter. Deux cas d'une même cause doivent rendre le même
// message, deux causes distinctes jamais le même — c'est ce couple, et lui
// seul, qui retient les trois branches de refusDeLAdresse.
func lesRefusDeSaisie() []struct{ nom, adresse, cause string } {
	return []struct{ nom, adresse, cause string }{
		{"vide", "", "saisie absente"},
		{"blancs seuls", "   ", "saisie absente"},
		{"relative", "/recettes/gratin", "pas une adresse de page"},
		{"sans schéma", "fourneaux-de-perlimpinpin.example/gratin", "pas une adresse de page"},
		{"schéma ftp", "ftp://fourneaux-de-perlimpinpin.example/gratin", "schéma non suivi"},
		{"schéma file", "file:///etc/passwd", "schéma non suivi"},
	}
}

// Critère 8, second volet : chaque cause de refus de la saisie a son message,
// aucune n'en partage un avec une autre, et aucun ne dit « une erreur est
// survenue ». C'est l'intention que TestChaqueCauseRendUnMessageQuiLuiEstPropre
// sert pour les causes de récupération, appliquée en amont, à la saisie.
//
// refusDeLAdresse est appelée directement, sur l'adresse ébarbée comme importe
// l'ébarbe : c'est elle qui distingue les causes, et la traverser par le réseau
// ne dirait rien de plus.
func TestChaqueRefusDeSaisieRendUnMessageQuiLuiEstPropre(t *testing.T) {
	parCause := map[string]string{}
	parMessage := map[string]string{}

	for _, c := range lesRefusDeSaisie() {
		t.Run(c.nom, func(t *testing.T) {
			message := refusDeLAdresse(strings.TrimSpace(c.adresse))

			if message == "" {
				t.Fatalf("%q n'est pas refusée", c.adresse)
			}
			if strings.Contains(strings.ToLower(message), "une erreur est survenue") {
				t.Errorf("message creux pour %q : %q", c.nom, message)
			}
			if deja, vue := parCause[c.cause]; vue && deja != message {
				t.Errorf("la cause %q rend %q pour %q, mais %q ailleurs", c.cause, message, c.nom, deja)
			}
			if autre, pris := parMessage[message]; pris && autre != c.cause {
				t.Errorf("les causes %q et %q rendent le même message : %q", c.cause, autre, message)
			}
			parCause[c.cause] = message
			parMessage[message] = c.cause
		})
	}
}

// Critère 9 : sans session, ni formulaire pré-rempli ni requête sortante. La
// route déclenche un appel depuis le serveur vers une URL choisie par
// l'appelant : ouverte, elle offrirait ce mandataire à qui la trouve.
func TestImportExigeUneSession(t *testing.T) {
	cas := []struct {
		nom  string
		joue func(http.Handler) *httptest.ResponseRecorder
	}{
		{"GET", func(mux http.Handler) *httptest.ResponseRecorder {
			return demande(mux, "/recettes/importer", nil, nil)
		}},
		{"POST", func(mux http.Handler) *httptest.ResponseRecorder {
			return importeLURL(mux, nil, urlSource, nil)
		}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux := serveurDeTest(t)
			compteParDefaut(t, app)
			reseauPiege(t)

			rec := c.joue(mux)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
			}
			if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
				t.Errorf("redirection vers %q, attendue /connexion", lieu)
			}
			if corps := rec.Body.String(); estLeFormulaireDeRecette(corps) {
				t.Errorf("un formulaire pré-rempli a été rendu à un visiteur :\n%s", corps)
			}
		})
	}
}

// Critère 7 : aucun chemin de l'import n'écrit en base. La création reste le
// geste de l'utilisateur, sur le formulaire.
func TestImportNeCreeAucunEnregistrement(t *testing.T) {
	cas := []cause{
		{"succès", func(t *testing.T) { sert(t, pageDuCorpus(t, "graphe-imbrique"), urlSource) }},
	}
	cas = append(cas, lesCauses()...)

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux, cookie := carnetDeTest(t)
			c.prepare(t)

			avant := compteLesEnregistrements(t, app)
			corpsImporte(t, mux, cookie)
			apres := compteLesEnregistrements(t, app)

			if !slices.Equal(avant, apres) {
				t.Errorf("enregistrements %v après l'import, %v avant", apres, avant)
			}
		})
	}
}

// compteLesEnregistrements rend le nombre de recettes et d'ingrédients.
func compteLesEnregistrements(t *testing.T, app core.App) []int64 {
	t.Helper()

	comptes := make([]int64, 0, 2)
	for _, collection := range []string{"recipes", "ingredients"} {
		compte, err := app.CountRecords(collection)
		if err != nil {
			t.Fatalf("comptage de %q : %v", collection, err)
		}
		comptes = append(comptes, compte)
	}
	return comptes
}

// --- Le rendu ---------------------------------------------------------------

// La page qui porte le champ « coller l'URL ».
func TestPageDImportPorteLeChampURL(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	reseauPiege(t)

	rec := demande(mux, "/recettes/importer", cookie, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	corps := rec.Body.String()
	if !strings.Contains(corps, `name="url"`) {
		t.Errorf("le champ « coller l'URL » est absent :\n%s", corps)
	}
	if !strings.Contains(corps, `action="/recettes/importer"`) {
		t.Errorf("le formulaire ne poste pas sur /recettes/importer :\n%s", corps)
	}
}

// Critère 13, convention PATA-12 : fragment sous HX-Request, document complet
// sinon — pour les deux routes.
func TestImportSuitLaConventionDeRendu(t *testing.T) {
	cas := []struct {
		nom  string
		joue func(http.Handler, *http.Cookie, map[string]string) *httptest.ResponseRecorder
	}{
		{"page d'import", func(mux http.Handler, cookie *http.Cookie, entetes map[string]string) *httptest.ResponseRecorder {
			return demande(mux, "/recettes/importer", cookie, entetes)
		}},
		{"import joué", func(mux http.Handler, cookie *http.Cookie, entetes map[string]string) *httptest.ResponseRecorder {
			return importeLURL(mux, cookie, urlSource, entetes)
		}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			_, mux, cookie := carnetDeTest(t)
			sert(t, pageDuCorpus(t, "graphe-imbrique"), urlSource)

			fragment := c.joue(mux, cookie, map[string]string{"HX-Request": "true"}).Body.String()
			if strings.Contains(fragment, "<html") || strings.Contains(fragment, "<body") {
				t.Errorf("un document complet a été rendu à HTMX :\n%s", fragment)
			}

			document := c.joue(mux, cookie, nil).Body.String()
			if !strings.Contains(document, "<html") || !strings.Contains(document, "<body") {
				t.Errorf("un fragment a été rendu hors HTMX :\n%s", document)
			}
		})
	}
}

// La route d'API a disparu au profit des deux routes en français : deux
// adresses pour le même geste finiraient par diverger.
func TestLAncienneRouteDApiNExistePlus(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	reseauPiege(t)

	req := httptest.NewRequest(http.MethodPost, "/api/import", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d pour POST /api/import, attendu %d", rec.Code, http.StatusNotFound)
	}
}

// --- Le plafond de débit ---------------------------------------------------

// seuilDeLImport est le nombre de POST /recettes/importer qu'une même adresse
// a le droit de jouer dans la fenêtre.
//
// Écrit en clair, et non relu depuis la migration : un test qui compare une
// constante à elle-même ne vérifie que lui-même. C'est ce chiffre-là que
// DOD.md §3 demande de garder sous test — « une valeur codée sans test finit
// augmentée temporairement ».
const seuilDeLImport = 10

// importeDepuis poste une adresse sur la route d'import depuis l'adresse
// donnée — importeLURL laisse celle que httptest pose pour tout le monde, et le
// limiteur compte par e.RealIP().
func importeDepuis(mux http.Handler, ip string, cookie *http.Cookie, adresse string) *httptest.ResponseRecorder {
	corps := leJetonEstPose(url.Values{"url": {adresse}}).Encode()
	req := httptest.NewRequest(http.MethodPost, "/recettes/importer", strings.NewReader(corps))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieDuJetonDeTest())
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// recuperateurCompte sert la même page à chaque appel et compte les requêtes
// sortantes. C'est ce compteur qui distingue « refusé » de « refusé après
// coup » : un plafond qui tomberait après le gestionnaire aurait déjà dérangé
// le site tiers, c'est-à-dire exactement ce que ce plafond existe pour éviter.
func recuperateurCompte(t *testing.T) *atomic.Int64 {
	t.Helper()

	var appels atomic.Int64
	avecRecuperateur(t, func(_ context.Context, _ string, _ ...recuperation.Option) (recuperation.Page, error) {
		appels.Add(1)
		return recuperation.Page{
			Corps:       pageDuCorpus(t, "graphe-imbrique"),
			URLFinale:   urlSource,
			TypeContenu: "text/html",
		}, nil
	})
	return &appels
}

// Chaque import déclenche deux requêtes sortantes vers le site visé, sous notre
// adresse et notre nom, et rien n'en bornait le nombre : le onzième import
// d'une minute est refusé, le dixième ne l'est pas.
//
// Et il est refusé *avant* le gestionnaire, donc avant que quoi que ce soit ne
// parte : le compteur de requêtes sortantes ne bouge pas, et le corps rendu ne
// porte pas la fiche pré-remplie que seul importe pose.
func TestLeOnziemeImportDUneMinuteEstRefuse(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	appels := recuperateurCompte(t)

	const ip = "203.0.113.30"

	for tentative := 1; tentative <= seuilDeLImport; tentative++ {
		rec := importeDepuis(mux, ip, cookie, urlSource)
		if rec.Code != http.StatusOK {
			t.Fatalf("import %d : statut %d, attendu %d — le plafond tombe avant le seuil",
				tentative, rec.Code, http.StatusOK)
		}
	}

	partiesAvant := appels.Load()

	rec := importeDepuis(mux, ip, cookie, urlSource)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("import %d : statut %d, attendu %d — la route d'import n'a pas de plafond",
			seuilDeLImport+1, rec.Code, http.StatusTooManyRequests)
	}
	if parties := appels.Load(); parties != partiesAvant {
		t.Errorf("%d requêtes sortantes après l'import refusé, attendu %d : le site tiers a été dérangé malgré le refus",
			parties, partiesAvant)
	}
	if estLeFormulaireDeRecette(rec.Body.String()) {
		t.Errorf("la fiche pré-remplie est rendue sur un import refusé : le gestionnaire a été atteint :\n%s", rec.Body.String())
	}
}

// Le dépassement de PocketBase sort en JSON, écrit par router.ErrorHandler. Or
// /recettes/importer est un formulaire HTML ordinaire, comme la page du lot :
// celui qui colle une adresse de trop verrait du JSON brut à la place de sa
// page — et son adresse serait perdue avec.
func TestLeDepassementDeLImportRendLaPageEnHTML(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	recuperateurCompte(t)

	rec := epuiseLePlafondDeLImport(t, mux, cookie, "203.0.113.31", urlSource)

	if typeDeContenu := rec.Header().Get("Content-Type"); !strings.Contains(typeDeContenu, "text/html") {
		t.Errorf("Content-Type %q, attendu du text/html : le dépassement est rendu en JSON", typeDeContenu)
	}
	exigeContient(t, rec.Body.String(), `name="url"`, `role="alert"`)

	if message := entreBalises(rec.Body.String(), `<p class="erreur" role="alert">`, "</p>"); strings.TrimSpace(message) == "" {
		t.Errorf("aucun message dans la page de dépassement :\n%s", rec.Body.String())
	}
	// « Le champ se re-rend avec ce que l'utilisateur a tapé » vaut aussi quand
	// c'est le plafond qui refuse.
	if saisie := valeurDe(t, rec.Body.String(), "url"); saisie != urlSource {
		t.Errorf("champ url %q après le dépassement, attendu %q : l'adresse saisie est perdue", saisie, urlSource)
	}
}

// La page de dépassement rend une saisie que le gestionnaire n'a jamais lue :
// le rattrapage tombe avant importe, et l'adresse reprise dans le champ n'a
// donc traversé ni refusDeLAdresse ni le gestionnaire. C'est une chaîne
// arbitraire du client qui ressort dans une page, et DOD.md §3 lui demande son
// test d'échappement : le gabarit ne dispense pas du test, puisque rien ne
// garantit de l'extérieur que le rattrapage emprunte le même que le refus
// ordinaire. Le pendant du lot est TestLaSaisieRepriseAuDepassementEstEchappee.
func TestLaSaisieRepriseAuDepassementDeLImportEstEchappee(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	// Une saisie que refusDeLAdresse écarte : rien ne doit partir, ni avant le
	// plafond ni au dépassement.
	reseauPiege(t)

	const injection = `<script>alert(1)</script>`

	corps := epuiseLePlafondDeLImport(t, mux, cookie, "203.0.113.35", injection).Body.String()

	if strings.Contains(corps, injection) {
		t.Errorf("la saisie reprise ressort telle quelle :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("la saisie reprise n'apparaît pas échappée :\n%s", corps)
	}
}

// L'étiquette de la règle porte la méthode, et pas seulement le chemin : la
// page « coller l'URL » n'envoie rien sur le réseau, et la plafonner mettrait
// le formulaire hors de portée de celui qui vient de dépasser son quota —
// précisément la page qu'il doit voir.
func TestLaPageDImportNEstPasPlafonnee(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	reseauPiege(t)

	const ip = "203.0.113.32"

	for appel := 1; appel <= seuilDeLImport+5; appel++ {
		rec := demandeDepuis(mux, ip, "/recettes/importer", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("appel %d de la page d'import : statut %d, attendu %d", appel, rec.Code, http.StatusOK)
		}
	}
}

// Le compteur est l'adresse du client, quelle que soit l'audience de la règle.
// Un plafond qui enfermerait tout le monde dès qu'une adresse le dépasse
// remplacerait l'abus d'un tiers par un déni de service chez nous.
func TestLePlafondDeLImportNEnfermePasLesAutresAdresses(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)
	recuperateurCompte(t)

	epuiseLePlafondDeLImport(t, mux, cookie, "203.0.113.33", urlSource)

	rec := importeDepuis(mux, "203.0.113.34", cookie, urlSource)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour une seconde adresse, attendu %d : le plafond d'une adresse ferme la porte aux autres",
			rec.Code, http.StatusOK)
	}
	if !estLeFormulaireDeRecette(rec.Body.String()) {
		t.Errorf("la fiche pré-remplie n'est pas rendue à la seconde adresse : son import n'a pas abouti :\n%s", rec.Body.String())
	}
}

// epuiseLePlafondDeLImport joue un import de trop depuis la même adresse et
// rend la réponse au dépassement.
func epuiseLePlafondDeLImport(t *testing.T, mux http.Handler, cookie *http.Cookie, ip, adresse string) *httptest.ResponseRecorder {
	t.Helper()

	var rec *httptest.ResponseRecorder
	for tentative := 1; tentative <= seuilDeLImport+1; tentative++ {
		rec = importeDepuis(mux, ip, cookie, adresse)
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("statut %d après %d imports depuis %s, attendu %d",
			rec.Code, seuilDeLImport+1, ip, http.StatusTooManyRequests)
	}
	return rec
}

// Le plafond porte sur POST /recettes/importer, et sur lui seul. La seule
// forme d'étiquette qui attraperait POST /recettes/{id} est le préfixe
// « POST /recettes/ », qui plafonnerait du même coup l'ajout, la modification
// et la suppression d'un commentaire et la suppression d'une recette — quatre
// routes qu'aucun constat ne vise. Ce qui borne le rythme sortant de ces deux
// routes-là, c'est la cadence partagée, pas un plafond de débit.
func TestLEnregistrementDUneRecetteNEstPasPlafonne(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	reseauPiege(t)

	for creation := 1; creation <= seuilDeLImport+2; creation++ {
		rec := poste(t, mux, "/recettes", cookie, champsValides())
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("création %d : statut %d, attendu %d — POST /recettes est plafonnée",
				creation, rec.Code, http.StatusSeeOther)
		}
	}

	premiere := recettes(t, app)[0]
	for edition := 1; edition <= seuilDeLImport+2; edition++ {
		rec := poste(t, mux, "/recettes/"+premiere.Id, cookie, champsValides())
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("édition %d : statut %d, attendu %d — POST /recettes/{id} est plafonnée",
				edition, rec.Code, http.StatusSeeOther)
		}
	}
}
