package main

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/Pol128/Patachoo/internal/texte"
)

// --- Le montage ------------------------------------------------------------

// tagPartage rend le tag de ce nom, en le créant au premier appel seulement.
//
// creeTags (recettes_test.go) crée sans chercher : deux recettes d'un même
// test portant le même tag s'y heurteraient à l'index d'unicité du slug, et
// c'est justement le cas que le filtre demande.
func tagPartage(t *testing.T, app core.App, nom string) *core.Record {
	t.Helper()

	if existant, err := app.FindFirstRecordByData("tags", "slug", texte.Slug(nom)); err == nil {
		return existant
	}

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}
	tag := core.NewRecord(collection)
	tag.Set("name", nom)
	if err := app.Save(tag); err != nil {
		t.Fatalf("création du tag %q : %v", nom, err)
	}
	return tag
}

// recetteEtiquetee enregistre une recette portant ces tags, dans cet ordre.
func recetteEtiquetee(t *testing.T, app core.App, titre string, noms ...string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}

	ids := make([]string, 0, len(noms))
	for _, nom := range noms {
		ids = append(ids, tagPartage(t, app, nom).Id)
	}

	recette := core.NewRecord(collection)
	recette.Set("title", titre)
	recette.Set("tags", ids)
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette %q : %v", titre, err)
	}
	return recette
}

// suggestionsPour joue la route de suggestions sur cette saisie et rend le
// corps de la réponse.
func suggestionsPour(t *testing.T, mux http.Handler, cookie *http.Cookie, saisie string) string {
	t.Helper()

	rec := demande(mux, "/tags/suggestions?"+url.Values{"tags": {saisie}}.Encode(), cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour la saisie %q, attendu %d", rec.Code, saisie, http.StatusOK)
	}
	return rec.Body.String()
}

// blocDeSuggestions isole la liste des suggestions du champ qui la précède :
// la saisie courante est rendue dans la valeur du champ, et une assertion
// portée sur la page entière la confondrait avec une suggestion.
//
// L'absence du bloc est une panne, pas un bloc vide : sans ce garde-fou, une
// assertion « aucune suggestion » passerait aussi si le rendu perdait la liste
// entière.
func blocDeSuggestions(t *testing.T, corps string) string {
	t.Helper()

	const ouvrante = `<ul id="suggestions-de-tags"`
	if !strings.Contains(corps, ouvrante) {
		t.Fatalf("bloc de suggestions absent du rendu :\n%s", corps)
	}
	return entreBalises(corps, ouvrante, `</ul>`)
}

// valeurDuChampTags rend l'attribut value du champ de saisie des tags.
func valeurDuChampTags(t *testing.T, corps string) string {
	t.Helper()

	trouve := regexp.MustCompile(`name="tags"[^>]*value="([^"]*)"`).FindStringSubmatch(corps)
	if trouve == nil {
		t.Fatalf("champ de tags introuvable :\n%s", corps)
	}
	return trouve[1]
}

// --- L'écriture des tags par le formulaire ---------------------------------

func TestUnPostCreeLesTagsNommesDansLaSaisie(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	champs := champsValides()
	champs.Set("tags", "Végétarien, plat unique")
	poste(t, mux, "/recettes", cookie, champs)

	recette := laRecette(t, app)
	tags, err := app.FindRecordsByIds("tags", recette.GetStringSlice("tags"))
	if err != nil {
		t.Fatalf("lecture des tags de la recette : %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("%d tag(s) liés à la recette, attendu 2", len(tags))
	}

	obtenus := map[string]bool{}
	for _, tag := range tags {
		obtenus[tag.GetString("name")] = true
	}
	for _, attendu := range []string{"végétarien", "plat unique"} {
		if !obtenus[attendu] {
			t.Errorf("tag %q absent de la recette : %v", attendu, obtenus)
		}
	}
}

// Le second envoi écrit « vegetarien », sans accent : c'est le même tag, et la
// collection ne doit pas en compter un troisième.
func TestUnSecondPostReutiliseLeTagExistant(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)

	premier := champsValides()
	premier.Set("tags", "Végétarien, plat unique")
	poste(t, mux, "/recettes", cookie, premier)

	second := champsValides()
	second.Set("titre", "Gratin de courgettes")
	second.Set("tags", "vegetarien")
	poste(t, mux, "/recettes", cookie, second)

	if n := nombreDeTags(t, app); n != 2 {
		t.Errorf("%d tag(s) en base après le second envoi, attendu 2", n)
	}
}

func TestLeFormulaireDEditionPreRemplitLesTagsDansLeurOrdre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	// L'ordre des tags de la recette est l'inverse de leur ordre de création :
	// un pré-remplissage qui suivrait la base plutôt que la recette rendrait
	// « plat unique, végétarien ».
	tagPartage(t, app, "végétarien")
	recette := recetteEtiquetee(t, app, "Gratin", "plat unique", "végétarien")

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()

	if valeur := valeurDuChampTags(t, corps); valeur != "plat unique, végétarien" {
		t.Errorf("champ de tags %q, attendu %q", valeur, "plat unique, végétarien")
	}
}

func TestUneEditionQuiVideLeChampRetireLesTags(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEtiquetee(t, app, "Gratin", "végétarien", "plat unique")

	champs := champsValides()
	champs.Set("titre", "Gratin")
	champs.Set("tags", "")
	poste(t, mux, "/recettes/"+recette.Id, cookie, champs)

	if tags := relitLaRecette(t, app, recette.Id).GetStringSlice("tags"); len(tags) != 0 {
		t.Errorf("%d tag(s) encore liés à la recette, attendu 0", len(tags))
	}
}

// --- Les suggestions -------------------------------------------------------

// La recherche se fait sur le slug, qui est déjà sans accent ni casse : « vég »
// et « veg » désignent le même tag, sans rien attendre de FTS5.
func TestLesSuggestionsCherchentSurLeSlug(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, "végétarien")
	tagPartage(t, app, "plat unique")

	for _, saisie := range []string{"vég", "veg", "VÉG"} {
		t.Run(saisie, func(t *testing.T) {
			bloc := blocDeSuggestions(t, suggestionsPour(t, mux, cookie, saisie))

			if !strings.Contains(bloc, "végétarien") {
				t.Errorf("« végétarien » n'est pas proposé sur %q :\n%s", saisie, bloc)
			}
			if strings.Contains(bloc, "plat unique") {
				t.Errorf("« plat unique » est proposé sur %q :\n%s", saisie, bloc)
			}
		})
	}
}

func TestUnTagDejaDansLaSaisieNEstPasPropose(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, "végétarien")
	tagPartage(t, app, "vegan")

	bloc := blocDeSuggestions(t, suggestionsPour(t, mux, cookie, "vegan, veg"))

	if !strings.Contains(bloc, "végétarien") {
		t.Errorf("« végétarien » n'est pas proposé :\n%s", bloc)
	}
	if strings.Contains(bloc, "vegan") {
		t.Errorf("« vegan » est proposé alors qu'il est déjà saisi :\n%s", bloc)
	}
}

// Seul ce qui suit la dernière virgule compte : la saisie entière donnerait le
// slug « vegetarien-pl », qui ne correspond à rien.
func TestSeulLeDernierFragmentCompte(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, "végétarien")
	tagPartage(t, app, "plat unique")

	bloc := blocDeSuggestions(t, suggestionsPour(t, mux, cookie, "végétarien, pl"))

	if !strings.Contains(bloc, "plat unique") {
		t.Errorf("« plat unique » n'est pas proposé sur « végétarien, pl » :\n%s", bloc)
	}
}

func TestUneSaisieSansFragmentNeProposeRien(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	for _, saisie := range []string{"", "   ", "végétarien, "} {
		t.Run(fmt.Sprintf("%q", saisie), func(t *testing.T) {
			bloc := blocDeSuggestions(t, suggestionsPour(t, mux, cookie, saisie))
			if strings.Contains(bloc, "<button") {
				t.Errorf("des suggestions sont rendues sur une saisie sans fragment :\n%s", bloc)
			}
		})
	}
}

func TestAuDelaDeHuitTagsHuitSontRendus(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	for i := 0; i < 12; i++ {
		tagPartage(t, app, fmt.Sprintf("plat %d", i))
	}

	bloc := blocDeSuggestions(t, suggestionsPour(t, mux, cookie, "pl"))

	if compte := strings.Count(bloc, "<button"); compte != 8 {
		t.Errorf("%d suggestions rendues, attendu 8 :\n%s", compte, bloc)
	}
}

// Le recollage se fait côté serveur : le bouton renvoie le slug retenu et la
// saisie courante, le serveur rend le champ complété.
func TestChoisirUneSuggestionRecolleLaSaisie(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, "plat unique")

	cible := "/tags/suggestions?" + url.Values{
		"tags":  {"végétarien, pl"},
		"choix": {"plat-unique"},
	}.Encode()
	rec := demande(mux, cible, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	corps := rec.Body.String()
	if valeur := valeurDuChampTags(t, corps); valeur != "végétarien, plat unique, " {
		t.Errorf("champ recollé %q, attendu %q", valeur, "végétarien, plat unique, ")
	}
	// Sans autofocus, la saisie repart du début de la page à chaque choix.
	if !strings.Contains(corps, "autofocus") {
		t.Errorf("le champ re-rendu ne porte pas autofocus :\n%s", corps)
	}
}

// Un slug qui ne désigne plus rien — le tag a été fusionné pendant que la page
// restait ouverte — rend la saisie telle quelle : ni recollée, ni amputée de
// son dernier fragment, et sans 500.
//
// Le tag « plat unique » est en base : sans la branche sql.ErrNoRows, le
// chemin des suggestions reprendrait la main et « pl » en proposerait une.
func TestChoisirUnSlugInexistantRendLaSaisieInchangee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, "plat unique")

	cible := "/tags/suggestions?" + url.Values{
		"tags":  {"végétarien, pl"},
		"choix": {"slug-inexistant"},
	}.Encode()
	rec := demande(mux, cible, cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	corps := rec.Body.String()
	if valeur := valeurDuChampTags(t, corps); valeur != "végétarien, pl" {
		t.Errorf("champ rendu %q, attendu la saisie inchangée %q", valeur, "végétarien, pl")
	}
	if bloc := blocDeSuggestions(t, corps); strings.Contains(bloc, "<button") {
		t.Errorf("une suggestion est rendue alors que le slug ne désigne rien :\n%s", bloc)
	}
}

// Le bloc est un fragment : une page complète renvoyée dans un hx-target
// produirait des pages imbriquées.
func TestLeBlocDeSuggestionsEstUnFragment(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, "végétarien")

	corps := suggestionsPour(t, mux, cookie, "vég")

	exigeSansAucun(t, corps, "<html", "<body")
	exigeContient(t, corps, "végétarien")
}

// Le champ reste soumissible sans JavaScript : l'autocomplétion est un
// confort, pas le chemin.
func TestLeChampDeTagsResteUnChampDeTexte(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", cookie).Body.String()

	exigeContient(t, corps,
		`type="text"`,
		`name="tags"`,
		`hx-get="/tags/suggestions"`,
	)
}

// --- Les suggestions : la session, dans les deux sens ----------------------

func TestLesSuggestionsExigentUneSession(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	tagPartage(t, app, "végétarien")
	cible := "/tags/suggestions?" + url.Values{"tags": {"vég"}}.Encode()

	t.Run("sans session", func(t *testing.T) {
		rec := demande(mux, cible, nil, nil)

		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusFound {
			t.Errorf("statut %d, attendue une redirection", rec.Code)
		}
		if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
			t.Errorf("Location %q, attendu %q", lieu, "/connexion")
		}
		if strings.Contains(rec.Body.String(), "végétarien") {
			t.Errorf("un nom de tag est rendu à un visiteur :\n%s", rec.Body.String())
		}
	})

	t.Run("avec session", func(t *testing.T) {
		cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

		rec := demande(mux, cible, cookie, nil)

		if rec.Code != http.StatusOK {
			t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
		}
		if !strings.Contains(blocDeSuggestions(t, rec.Body.String()), "végétarien") {
			t.Errorf("aucune suggestion rendue à un compte connecté :\n%s", rec.Body.String())
		}
	})
}

// --- Le filtre par tag -----------------------------------------------------

// Une recette à deux tags dont un seul correspond doit rester : c'est ce qui
// distingue « au moins un tag » de « tous les tags », et un « = » écrit à la
// place de « ?= » rougit ici.
func TestLeFiltreNeRendQueLesRecettesPortantLeTag(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien", "plat unique")
	recetteEtiquetee(t, app, "Clafoutis aux cerises", "dessert")

	corps := listeDe(t, mux, cookie, "/recettes?tag=vegetarien")

	exigeContient(t, corps, "Gratin de courgettes")
	exigeSansAucun(t, corps, "Clafoutis aux cerises")
}

func TestLeTagEtLaRechercheSeCombinent(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")
	recetteEtiquetee(t, app, "Gratin dauphinois", "plat unique")

	// Sans le tag, les deux gratins répondent : c'est le filtre, et lui seul,
	// qui n'en laisse qu'un.
	sansTag := listeDe(t, mux, cookie, "/recettes?q=gratin")
	exigeContient(t, sansTag, "Gratin de courgettes", "Gratin dauphinois")

	corps := listeDe(t, mux, cookie, "/recettes?q=gratin&tag=vegetarien")

	exigeContient(t, corps, "Gratin de courgettes")
	exigeSansAucun(t, corps, "Gratin dauphinois")
}

func TestLaPaginationConserveLeTagEtLeTerme(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	for i := 0; i < 25; i++ {
		recetteEtiquetee(t, app, numero(i), "végétarien")
	}

	premiere := listeDe(t, mux, cookie, "/recettes?q=numero&tag=vegetarien")
	exigeContient(t, premiere, `rel="next" href="/recettes?page=2&amp;q=numero&amp;tag=vegetarien"`)

	seconde := listeDe(t, mux, cookie, "/recettes?q=numero&tag=vegetarien&page=2")
	exigeContient(t, seconde, `rel="prev" href="/recettes?q=numero&amp;tag=vegetarien"`)
}

// Un slug qui ne désigne aucun tag rend une liste vide : ni 500, ni carnet
// entier.
func TestUnTagInconnuRendUneListeVide(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")

	rec := demande(mux, "/recettes?tag=nexistepas", cookie, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	corps := rec.Body.String()
	exigeSansAucun(t, corps, "Gratin de courgettes")
	exigeContient(t, corps, `class="absence"`)
}

// --- Les tags cliquables ---------------------------------------------------

func TestLaVignetteRendChaqueTagEnLien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeContient(t, corps, `href="/recettes?tag=vegetarien"`, "végétarien")
}

func TestLaFicheRendChaqueTagEnLien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id, cookie).Body.String()

	exigeContient(t, corps, `href="/recettes?tag=vegetarien"`, "végétarien")
}

// --- Le filtre actif se voit et se retire ----------------------------------

func TestLeFiltreActifSeNommeEtSeRetire(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")

	corps := listeDe(t, mux, cookie, "/recettes?q=gratin&tag=vegetarien")

	filtre := entreBalises(corps, `<p class="filtre">`, "</p>")
	if filtre == "" {
		t.Fatalf("aucun bloc de filtre actif :\n%s", corps)
	}
	if !strings.Contains(filtre, "végétarien") {
		t.Errorf("le filtre actif ne nomme pas le tag : %q", filtre)
	}
	// Le terme est conservé, le tag seul est retiré : sinon un filtre posé
	// d'un clic emporte la recherche avec lui.
	if !strings.Contains(filtre, `href="/recettes?q=gratin"`) {
		t.Errorf("le filtre actif n'offre pas de sortie qui conserve la recherche : %q", filtre)
	}
}

func TestSansTagAucunBlocDeFiltre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")

	corps := listeDe(t, mux, cookie, "/recettes")

	exigeSansAucun(t, corps, `<p class="filtre">`)
}

// --- Échappement (DOD.md §3) ----------------------------------------------

// Le nom d'un tag ressort dans quatre rendus différents. La vignette et la
// fiche sont déjà couvertes par TestLaGrilleEchappeLesTags et
// TestLaFicheEchappeToutCeQuiVientDuDehors ; restent les deux chemins que
// cette tâche ajoute.

func TestLeChampDeTagsEchappeLesNoms(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEtiquetee(t, app, "Gratin", `"><script>alert(1)</script>`)

	corps := avecCookie(mux, http.MethodGet, "/recettes/"+recette.Id+"/modifier", cookie).Body.String()

	exigeSansAucun(t, corps, `"><script>alert(1)</script>`)
	if valeur := valeurDuChampTags(t, corps); !strings.Contains(valeur, "&lt;script&gt;") {
		t.Errorf("nom de tag non échappé dans le champ : %q", valeur)
	}
}

func TestLeBlocDeSuggestionsEchappeLesNoms(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	tagPartage(t, app, `"><script>alert(1)</script>`)

	bloc := blocDeSuggestions(t, suggestionsPour(t, mux, cookie, "script"))

	if strings.Contains(bloc, "<script>") {
		t.Errorf("balise script exécutable dans les suggestions :\n%s", bloc)
	}
	if !strings.Contains(bloc, "&lt;script&gt;") {
		t.Errorf("le nom du tag a disparu au lieu d'être échappé :\n%s", bloc)
	}
}

// Le slug reçu dans l'URL ressort tel quel dans trois rendus quand il ne
// désigne aucun tag — le champ caché du formulaire, le bandeau de filtre et le
// message d'absence. C'est du contenu réfléchi, au même titre que le terme de
// recherche : une seule requête les traverse tous les trois, puisqu'un tag
// inconnu ne rend aucune vignette.
func TestLeFiltreEchappeLeTagRecuDansLURL(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recetteEtiquetee(t, app, "Gratin de courgettes", "végétarien")

	const charge = `"><script>alert(1)</script>`
	corps := listeDe(t, mux, cookie, "/recettes?"+url.Values{"tag": {charge}}.Encode())

	exigeSansAucun(t, corps, `"><script>`, "<script>alert(1)</script>")

	champ := entreBalises(corps, `name="tag" value="`, `"`)
	if champ == "" {
		t.Fatalf("champ caché du tag introuvable :\n%s", corps)
	}
	if !strings.Contains(champ, "&lt;script&gt;") {
		t.Errorf("tag non échappé dans le champ caché : %q", champ)
	}

	bandeau := entreBalises(corps, `<p class="filtre">`, "</p>")
	if bandeau == "" {
		t.Fatalf("bandeau de filtre introuvable :\n%s", corps)
	}
	if !strings.Contains(bandeau, "&lt;script&gt;") {
		t.Errorf("tag non échappé dans le bandeau de filtre : %q", bandeau)
	}

	message := entreBalises(corps, `<p class="absence">`, "</p>")
	if message == "" {
		t.Fatalf("message d'absence introuvable :\n%s", corps)
	}
	if !strings.Contains(message, "&lt;script&gt;") {
		t.Errorf("tag non échappé dans le message d'absence : %q", message)
	}
}
