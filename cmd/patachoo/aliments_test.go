package main

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// La vue agrégée par aliment : un groupe par aliment canonique, et l'ordre
// dans lequel ces groupes se lisent.
//
// Ce qui est en cause ici n'est pas ce que l'ouvrier écrit — analyse_test.go
// le dit déjà — mais ce que la page ramène d'une table de formes déjà posée.
// Les formes sont donc écrites à la main : c'est le seul moyen de poser des
// occurrences, des signaux et des catégories choisis, et donc de savoir quel
// ordre est attendu.

// --- Fixtures ---------------------------------------------------------------

// formeDeTest est une ligne de analyses_formes telle qu'un test la veut.
type formeDeTest struct {
	brut        string
	aliment     string
	resolu      bool
	categorie   string
	occurrences int
	signaux     []string
}

// passeTermineeDeTest pose une analyse close et les formes qu'elle a données.
func passeTermineeDeTest(t *testing.T, app core.App, formes ...formeDeTest) *core.Record {
	t.Helper()
	return passeDeTest(t, app, statutTermine, formes...)
}

// passeDeTest pose une analyse dans le statut demandé, et ses formes.
func passeDeTest(t *testing.T, app core.App, statut string, formes ...formeDeTest) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses")
	if err != nil {
		t.Fatalf("collection analyses : %v", err)
	}
	passe := core.NewRecord(collection)
	passe.Set("source", sourceFournie)
	passe.Set("status", statut)
	passe.Set("forms", len(formes))
	if err := app.Save(passe); err != nil {
		t.Fatalf("écriture de la passe : %v", err)
	}

	collectionDesFormes, err := app.FindCollectionByNameOrId("analyses_formes")
	if err != nil {
		t.Fatalf("collection analyses_formes : %v", err)
	}
	for _, forme := range formes {
		enregistrement := core.NewRecord(collectionDesFormes)
		enregistrement.Set("analysis", passe.Id)
		enregistrement.Set("raw", forme.brut)
		enregistrement.Set("occurrences", forme.occurrences)
		enregistrement.Set("food", forme.aliment)
		enregistrement.Set("resolved", forme.resolu)
		enregistrement.Set("category", forme.categorie)
		// Jamais nil : la colonne part en JSON et json_array_length veut un
		// tableau des deux côtés, exactement comme signaux() le promet.
		signaux := forme.signaux
		if signaux == nil {
			signaux = []string{}
		}
		enregistrement.Set("signals", signaux)
		if err := app.Save(enregistrement); err != nil {
			t.Fatalf("écriture de la forme %q : %v", forme.brut, err)
		}
	}

	return passe
}

// formeResolue est le cas ordinaire : un aliment que le lexique connaît, une
// catégorie, et un signal qui le fait entrer dans l'ordre par défaut.
func formeResolue(brut, aliment, categorie string, occurrences int, signaux ...string) formeDeTest {
	return formeDeTest{
		brut:        brut,
		aliment:     aliment,
		resolu:      true,
		categorie:   categorie,
		occurrences: occurrences,
		signaux:     signaux,
	}
}

// formeNonResolue est l'autre tas : la clé est le texte d'aliment tel que lu,
// il n'y a pas de catégorie, et le signal « non résolu » est toujours allumé.
func formeNonResolue(brut, aliment string, occurrences int) formeDeTest {
	return formeDeTest{
		brut:        brut,
		aliment:     aliment,
		occurrences: occurrences,
		signaux:     []string{SignalNonResolu},
	}
}

// annotationDeGroupe tranche un groupe par sa clé, comme PATA-127 le fera :
// une ligne de analyses_annotations qui ne porte que l'aliment.
func annotationDeGroupe(t *testing.T, app core.App, passe *core.Record, aliment string) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	annotation := core.NewRecord(collection)
	annotation.Set("analysis", passe.Id)
	annotation.Set("food", aliment)
	annotation.Set("shareable_note", "vu")
	if err := app.Save(annotation); err != nil {
		t.Fatalf("écriture de l'annotation sur %q : %v", aliment, err)
	}
}

// recule antidate une passe, pour que « la dernière terminée » désigne
// quelqu'un sans ambiguïté : deux enregistrements créés dans la même
// milliseconde laisseraient le tri décider tout seul.
func recule(t *testing.T, app core.App, passe *core.Record, quand string) {
	t.Helper()

	_, err := app.DB().NewQuery("UPDATE analyses SET created = {:quand} WHERE id = {:id}").
		Bind(dbx.Params{"quand": quand, "id": passe.Id}).Execute()
	if err != nil {
		t.Fatalf("antidatage de la passe : %v", err)
	}
}

// --- Lecture de la page ------------------------------------------------------

// ligneDeGroupe reconnaît une ligne de la vue, et chacune de ses cellules.
//
// Une lecture textuelle et volontairement bête, comme celle de la feuille de
// style : ce qui est en cause est l'ordre des lignes et le contenu de leurs
// cellules, pas la mise en forme.
var ligneDeGroupe = regexp.MustCompile(
	`<tr class="groupe[^"]*"><td class="aliment-canonique">([^<]*)</td>` +
		`<td class="categorie">([^<]*)</td>` +
		`<td class="signaux">(.*?)</td>` +
		`<td class="formes">([^<]*)</td>` +
		`<td class="occurrences">([^<]*)</td></tr>`)

// groupeAffiche est ce qu'une ligne d'écran montre.
type groupeAffiche struct {
	Aliment     string
	Categorie   string
	Signaux     string
	Formes      string
	Occurrences string
}

// groupesAffiches rend les lignes de la vue, dans l'ordre où elles sont
// écrites — c'est cet ordre qui est le sujet de la tâche.
func groupesAffiches(corps string) []groupeAffiche {
	var lus []groupeAffiche
	for _, ligne := range ligneDeGroupe.FindAllStringSubmatch(corps, -1) {
		lus = append(lus, groupeAffiche{
			Aliment:     html.UnescapeString(ligne[1]),
			Categorie:   html.UnescapeString(ligne[2]),
			Signaux:     html.UnescapeString(ligne[3]),
			Formes:      ligne[4],
			Occurrences: ligne[5],
		})
	}
	return lus
}

// alimentsAffiches ne garde que la clé de chaque ligne, dans l'ordre.
func alimentsAffiches(corps string) []string {
	var noms []string
	for _, groupe := range groupesAffiches(corps) {
		noms = append(noms, groupe.Aliment)
	}
	return noms
}

// laVueAgregee demande la page avec la chaîne de requête donnée, et exige un
// 200 : un critère d'ordre ne se lit pas sur une page qui n'est pas rendue.
func laVueAgregee(t *testing.T, mux http.Handler, cookie *http.Cookie, requete url.Values) string {
	t.Helper()

	rec := vueAgregeeBrute(mux, cookie, requete)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur la vue agrégée, attendu %d — corps :\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// vueAgregeeBrute demande la même page sans rien exiger de son statut.
func vueAgregeeBrute(mux http.Handler, cookie *http.Cookie, requete url.Values) *httptest.ResponseRecorder {
	cible := cheminDesAlimentsDeLEtabli
	if len(requete) > 0 {
		cible += "?" + requete.Encode()
	}
	return avecCookie(mux, http.MethodGet, cible, cookie)
}

// exigeLOrdre compare l'ordre lu à l'ordre attendu, et dit les deux.
func exigeLOrdre(t *testing.T, corps string, attendu ...string) {
	t.Helper()

	lus := alimentsAffiches(corps)
	if strings.Join(lus, "|") != strings.Join(attendu, "|") {
		t.Errorf("ordre %v, attendu %v", lus, attendu)
	}
}

// --- L'ordre par défaut ------------------------------------------------------

// Le signal filtre, la fréquence classe : on ne garde que les groupes portant
// au moins un signal, puis on trie ce tas-là par occurrences décroissantes.
//
// Un groupe sans signal vu 9 000 fois est donc absent, et c'est tout le sujet
// du tri : les entrées les plus vues sont les mieux lues.
func TestLOrdreParDefautFiltreParSignalPuisClasseParOccurrences(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("1 pincée de sel", "sel", "Épices", 9000),
		formeResolue("2 oignons", "oignon", "Légumes", 40, SignalMotsPerdus),
		formeResolue("1 gousse de vanille", "gousse de vanille", "Épices", 400, SignalUniteRepetee),
		formeResolue("10 cl de crème", "crème fraîche", "Crèmerie", 120, SignalMotsPerdus),
	)

	corps := laVueAgregee(t, mux, cookie, nil)

	exigeLOrdre(t, corps, "gousse de vanille", "crème fraîche", "oignon")
}

// La liste complète reste atteignable, mais ne s'ouvre pas par défaut : la
// page porte son lien, et c'est ce lien qui ramène le groupe sans signal.
func TestLaListeCompleteRamèneLesGroupesSansSignalSansSOuvrirParDefaut(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("1 pincée de sel", "sel", "Épices", 9000),
		formeResolue("2 oignons", "oignon", "Légumes", 40, SignalMotsPerdus),
	)

	parDefaut := laVueAgregee(t, mux, cookie, nil)
	if !strings.Contains(parDefaut, parametreDeLaListeComplete+"="+valeurDeLaListeComplete) {
		t.Error("la page par défaut ne porte pas le lien vers la liste complète")
	}

	complete := laVueAgregee(t, mux, cookie,
		url.Values{parametreDeLaListeComplete: {valeurDeLaListeComplete}})

	exigeLOrdre(t, complete, "sel", "oignon")
}

// Ce qui rend un groupe « tranché », ici : sa clé porte au moins une ligne
// dans analyses_annotations. Il sort de l'ordre par défaut, et il revient dans
// la liste complète.
func TestUnGroupeTrancheSortDeLOrdreParDefautEtRevientDansLaListeComplete(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus),
		formeResolue("1 gousse de vanille", "gousse de vanille", "Épices", 40, SignalUniteRepetee),
	)
	annotationDeGroupe(t, app, passe, "oignon")

	exigeLOrdre(t, laVueAgregee(t, mux, cookie, nil), "gousse de vanille")
	exigeLOrdre(t, laVueAgregee(t, mux, cookie,
		url.Values{parametreDeLaListeComplete: {valeurDeLaListeComplete}}),
		"oignon", "gousse de vanille")
}

// --- Ce qu'une ligne montre --------------------------------------------------

// Les occurrences affichées somment exactement celles des formes du groupe, et
// le nombre de formes est le nombre de lignes de analyses_formes du groupe.
//
// Trois formes aux occurrences différentes : une somme fausse — le maximum, la
// première, le compte — ne peut pas tomber juste par hasard.
func TestLesOccurrencesSommentLesFormesEtLeCompteLesDenombre(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 300, SignalMotsPerdus),
		formeResolue("1 oignon jaune", "oignon", "Légumes", 70, SignalMotsPerdus),
		formeResolue("oignon émincé", "oignon", "Légumes", 5, SignalMotsPerdus),
	)

	groupes := groupesAffiches(laVueAgregee(t, mux, cookie, nil))
	if len(groupes) != 1 {
		t.Fatalf("%d groupe(s), attendu 1 : %v", len(groupes), groupes)
	}
	if groupes[0].Occurrences != "375" {
		t.Errorf("occurrences = %q, attendu %q — la somme du groupe", groupes[0].Occurrences, "375")
	}
	if groupes[0].Formes != "3" {
		t.Errorf("formes = %q, attendu %q — les lignes du groupe", groupes[0].Formes, "3")
	}
}

// Une ligne d'écran montre aussi la catégorie et les signaux, et les signaux
// sont écrits en français : « unite_repetee » est une clé de schéma, pas un
// mot qu'on montre.
func TestUneLigneMontreLaCategorieEtSesSignauxEnFrancais(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("1 gousse de vanille", "gousse de vanille", "Épices", 400, SignalUniteRepetee),
		formeResolue("2 gousses de vanille", "gousse de vanille", "Épices", 12, SignalMotsPerdus),
	)

	groupes := groupesAffiches(laVueAgregee(t, mux, cookie, nil))
	if len(groupes) != 1 {
		t.Fatalf("%d groupe(s), attendu 1 : %v", len(groupes), groupes)
	}
	if groupes[0].Categorie != "Épices" {
		t.Errorf("catégorie = %q, attendu %q", groupes[0].Categorie, "Épices")
	}
	// L'union des signaux du groupe, et non ceux de la première forme lue.
	for _, attendu := range []string{"unité répétée", "mots perdus"} {
		if !strings.Contains(groupes[0].Signaux, attendu) {
			t.Errorf("signaux = %q, sans %q", groupes[0].Signaux, attendu)
		}
	}
}

// --- Le tas des non résolus --------------------------------------------------

// Les aliments non résolus forment leur propre tas, sans catégorie : ils ne se
// mêlent pas aux groupes résolus, fussent-ils plus vus qu'eux.
func TestUnAlimentNonResoluFormeSonPropreTasSansCategorie(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeNonResolue("1 kg de fleur de sureau", "fleur de sureau", 4000),
		formeResolue("2 oignons", "oignon", "Légumes", 40, SignalMotsPerdus),
	)

	groupes := groupesAffiches(laVueAgregee(t, mux, cookie, nil))
	if len(groupes) != 2 {
		t.Fatalf("%d groupe(s), attendu 2 : %v", len(groupes), groupes)
	}
	// Le tas des résolus d'abord, celui des non résolus ensuite : 4 000
	// occurrences ne font pas passer un non résolu devant.
	if groupes[0].Aliment != "oignon" || groupes[1].Aliment != "fleur de sureau" {
		t.Errorf("ordre %v, attendu [oignon fleur de sureau] — les deux tas ne se mêlent pas",
			alimentsAffiches(laVueAgregee(t, mux, cookie, nil)))
	}
	if groupes[1].Categorie != "" {
		t.Errorf("catégorie = %q sur un non résolu, attendue vide", groupes[1].Categorie)
	}
}

// Le tas des non résolus ne se contente pas d'être séparé sur un écran : il
// l'est d'une page à l'autre. Sans ce premier terme dans l'ordre, un non
// résolu très vu remonterait sur la première page — au-dessous des résolus
// puisque la page les sépare, mais en prenant leur place.
//
// Le jeu est construit pour ça : les trois non résolus sont les plus vus de
// tous, et l'ordre par défaut classe par occurrences décroissantes.
func TestLeTasDesNonResolusRestePleinAPartirDeLaPageOuIlCommence(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	formes := make([]formeDeTest, 0, parPage+3)
	for i := range parPage {
		formes = append(formes, formeResolue(
			fmt.Sprintf("%d aliment %02d", i, i),
			fmt.Sprintf("aliment %02d", i),
			"Légumes",
			parPage-i,
			SignalMotsPerdus))
	}
	for i := range 3 {
		formes = append(formes, formeNonResolue(
			fmt.Sprintf("1 kg de inconnu %d", i),
			fmt.Sprintf("inconnu %d", i),
			1000-i))
	}
	passeTermineeDeTest(t, app, formes...)

	premiere := groupesAffiches(laVueAgregee(t, mux, cookie, nil))
	if len(premiere) != parPage {
		t.Fatalf("%d groupe(s) sur la première page, attendu %d", len(premiere), parPage)
	}
	for _, groupe := range premiere {
		if groupe.Categorie == "" {
			t.Errorf("%q, sans catégorie, occupe la première page : les deux tas se mêlent",
				groupe.Aliment)
		}
	}

	seconde := alimentsAffiches(laVueAgregee(t, mux, cookie, url.Values{parametreDeLaPage: {"2"}}))
	if strings.Join(seconde, "|") != "inconnu 0|inconnu 1|inconnu 2" {
		t.Errorf("seconde page %v, attendu les trois non résolus", seconde)
	}
}

// Le texte d'un aliment non résolu vient du corpus : il s'affiche
// littéralement et n'exécute rien (DoD §3).
func TestUnAlimentNonResoluPorteurDeBalisageRessortLitteralement(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	const malveillant = `<script>alert(1)</script>`
	passeTermineeDeTest(t, app,
		formeNonResolue("1 pincée de "+malveillant, malveillant, 3),
	)

	corps := laVueAgregee(t, mux, cookie, nil)

	if strings.Contains(corps, malveillant) {
		t.Error("la balise du corpus ressort telle quelle dans la page")
	}
	if !strings.Contains(corps, html.EscapeString(malveillant)) {
		t.Errorf("l'aliment non résolu n'est pas affiché échappé — corps :\n%s", corps)
	}
	groupes := groupesAffiches(corps)
	if len(groupes) != 1 || groupes[0].Aliment != malveillant {
		t.Errorf("aliment affiché %v, attendu le texte brut une fois déséchappé", groupes)
	}
}

// --- Les trois tris ----------------------------------------------------------

// Les trois tris sont atteignables par la chaîne de requête, et chacun rend un
// ordre qui diffère de celui par défaut — sans quoi le test ne dirait rien de
// plus que le tri qu'il double.
//
// Le jeu est construit pour ça : le groupe le plus vu n'est ni celui qui avale
// le plus de formes, ni le premier par catégorie, ni le plus rare.
func TestLesTroisTrisRendentChacunUnOrdreDifferentDuDefaut(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		// « oignon » avale trois formes, et totalise 375 occurrences.
		formeResolue("2 oignons", "oignon", "Légumes", 300, SignalMotsPerdus),
		formeResolue("1 oignon jaune", "oignon", "Légumes", 70, SignalMotsPerdus),
		formeResolue("oignon émincé", "oignon", "Légumes", 5, SignalMotsPerdus),
		// « gousse de vanille » n'en avale que deux, et se voit davantage.
		formeResolue("1 gousse de vanille", "gousse de vanille", "Épices", 380, SignalUniteRepetee),
		formeResolue("2 gousses de vanille", "gousse de vanille", "Épices", 20, SignalUniteRepetee),
		// « crème fraîche » n'en avale qu'une, et c'est la plus rare.
		formeResolue("10 cl de crème", "crème fraîche", "Crèmerie", 3, SignalMotsPerdus),
	)

	// Le signal filtre, la fréquence classe.
	exigeLOrdre(t, laVueAgregee(t, mux, cookie, nil),
		"gousse de vanille", "oignon", "crème fraîche")

	for nom, cas := range map[string]struct {
		tri     string
		attendu []string
	}{
		// Combien de formes brutes distinctes une entrée avale : met en tête
		// celles qui capturent le plus.
		"dispersion": {triParDispersion, []string{"oignon", "gousse de vanille", "crème fraîche"}},
		// Toutes les épices d'affilée. L'ordre entre catégories est celui de
		// SQLite — un classement d'octets, où « Épices » suit « Légumes » —
		// et ce n'est pas ce que le tri promet : ce qu'il promet est que les
		// libellés ne soient pas éparpillés, et ils ne le sont pas.
		"catégorie": {triParCategorie, []string{"crème fraîche", "oignon", "gousse de vanille"}},
		// Le balayage de fin.
		"rareté": {triParRarete, []string{"crème fraîche", "oignon", "gousse de vanille"}},
	} {
		t.Run(nom, func(t *testing.T) {
			exigeLOrdre(t, laVueAgregee(t, mux, cookie, url.Values{parametreDuTri: {cas.tri}}),
				cas.attendu...)
		})
	}
}

// Un tri inconnu, une page nulle, négative ou non numérique rendent la
// première page de l'ordre par défaut, sans erreur HTTP.
func TestUnParametreMalmeneRetombeSurLaPremierePageParDefaut(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus),
		formeResolue("10 cl de crème", "crème fraîche", "Crèmerie", 40, SignalMotsPerdus),
	)

	parDefaut := []string{"oignon", "crème fraîche"}
	for nom, requete := range map[string]url.Values{
		"tri inconnu":        {parametreDuTri: {"n-importe-quoi"}},
		"page nulle":         {parametreDeLaPage: {"0"}},
		"page négative":      {parametreDeLaPage: {"-3"}},
		"page non numérique": {parametreDeLaPage: {"septième"}},
		"page démesurée":     {parametreDeLaPage: {"999999999999999999"}},
		"analyse inconnue":   {parametreDeLAnalyse: {"pas-un-identifiant"}},
	} {
		t.Run(nom, func(t *testing.T) {
			rec := vueAgregeeBrute(mux, cookie, requete)
			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d, attendu %d — corps :\n%s",
					rec.Code, http.StatusOK, rec.Body.String())
			}
			if nom == "page démesurée" {
				// Celle-là est bornée, pas ramenée au début : ce qui compte
				// est qu'elle ne déborde pas et ne rende pas la première page
				// à qui a demandé la dernière.
				if groupes := groupesAffiches(rec.Body.String()); len(groupes) != 0 {
					t.Errorf("%d groupe(s) sur une page démesurée, attendu 0", len(groupes))
				}
				return
			}
			exigeLOrdre(t, rec.Body.String(), parDefaut...)
		})
	}
}

// --- La pagination -----------------------------------------------------------

// Un jeu qui dépasse une page rend la suivante atteignable, et la dernière
// n'en annonce pas d'autre. 112 297 formes font des milliers de groupes : la
// liste ne tient pas sur un écran.
func TestLaListeEstPagineeEtLaDernierePageNAnnoncePasDeSuivante(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	formes := make([]formeDeTest, 0, parPage+3)
	for i := range parPage + 3 {
		formes = append(formes, formeResolue(
			fmt.Sprintf("%d aliment %02d", i, i),
			fmt.Sprintf("aliment %02d", i),
			"Légumes",
			parPage+3-i,
			SignalMotsPerdus))
	}
	passeTermineeDeTest(t, app, formes...)

	premiere := laVueAgregee(t, mux, cookie, nil)
	if lus := alimentsAffiches(premiere); len(lus) != parPage {
		t.Fatalf("%d groupe(s) sur la première page, attendu %d", len(lus), parPage)
	}
	if !strings.Contains(premiere, `rel="next"`) {
		t.Error("la première page n'annonce pas de suivante alors que le jeu déborde")
	}

	seconde := laVueAgregee(t, mux, cookie, url.Values{parametreDeLaPage: {"2"}})
	if lus := alimentsAffiches(seconde); len(lus) != 3 {
		t.Errorf("%d groupe(s) sur la seconde page, attendu 3", len(lus))
	}
	if strings.Contains(seconde, `rel="next"`) {
		t.Error("la dernière page annonce une suivante")
	}
	if !strings.Contains(seconde, `rel="prev"`) {
		t.Error("la seconde page n'annonce pas de précédente")
	}
}

// --- La passe affichée -------------------------------------------------------

// La vue porte sur la dernière analyse terminée, qu'un paramètre d'URL permet
// de remplacer par l'identifiant d'une autre.
func TestLaVuePorteSurLaDerniereAnalyseTermineeSaufParametre(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	ancienne := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus))
	recule(t, app, ancienne, "2026-09-18 10:00:00.000Z")
	passeTermineeDeTest(t, app,
		formeResolue("10 cl de crème", "crème fraîche", "Crèmerie", 40, SignalMotsPerdus))

	exigeLOrdre(t, laVueAgregee(t, mux, cookie, nil), "crème fraîche")
	exigeLOrdre(t, laVueAgregee(t, mux, cookie,
		url.Values{parametreDeLAnalyse: {ancienne.Id}}), "oignon")
}

// Tant qu'aucune analyse n'est terminée, la page le dit et renvoie vers la
// page de lancement au lieu d'afficher une liste vide.
func TestSansAucuneAnalyseTermineeLaPageRenvoieAuLancement(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	// Une passe en cours ne compte pas : elle n'a pas fini d'écrire ses formes.
	passeDeTest(t, app, statutEnCours,
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus))

	corps := laVueAgregee(t, mux, cookie, nil)

	if groupes := groupesAffiches(corps); len(groupes) != 0 {
		t.Errorf("%d groupe(s) affiché(s) sans analyse terminée, attendu 0", len(groupes))
	}
	if !strings.Contains(corps, `href="`+cheminDeLEtabli+`"`) {
		t.Errorf("la page ne renvoie pas vers le lancement — corps :\n%s", corps)
	}
}

// --- Le droit d'entrer -------------------------------------------------------

// La vue est réservée aux comptes portant le droit d'accès à l'établi, et le
// refus se teste dans le sens du refus : un compte authentifié sans le droit
// est refusé, un visiteur est renvoyé se connecter.
func TestLaVueAgregeeEstReserveeAuxCurateurs(t *testing.T) {
	app, mux := serveurDeLEtabli(t)
	compteParDefaut(t, app)
	ordinaire := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

	for nom, attendu := range map[string]struct {
		cookie *http.Cookie
		statut int
	}{
		"visiteur":         {nil, http.StatusSeeOther},
		"compte ordinaire": {ordinaire, http.StatusForbidden},
	} {
		t.Run(nom, func(t *testing.T) {
			rec := vueAgregeeBrute(mux, attendu.cookie, nil)
			if rec.Code != attendu.statut {
				t.Fatalf("statut %d, attendu %d — corps :\n%s",
					rec.Code, attendu.statut, rec.Body.String())
			}
		})
	}
}

// La tâche n'ajoute aucune route en écriture : l'établi ne modifie ni recipes
// ni ingredients, et la vue agrégée ne répond qu'au GET.
func TestLaVueAgregeeNAjouteAucuneRouteEnEcriture(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus))

	avant := empreinteDes(t, app, "recipes", "ingredients")

	// Un 404 et non un 405 : aucun motif n'est enregistré pour ce couple
	// méthode/chemin, et c'est le gestionnaire d'absence de PocketBase qui
	// répond. Ce qui est en cause est qu'aucun gestionnaire ne soit atteint.
	rec := avecCookie(mux, http.MethodPost, cheminDesAlimentsDeLEtabli, cookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d sur un POST, attendu %d — corps :\n%s",
			rec.Code, http.StatusNotFound, rec.Body.String())
	}

	laVueAgregee(t, mux, cookie, nil)
	if apres := empreinteDes(t, app, "recipes", "ingredients"); apres != avant {
		t.Error("la vue agrégée a touché au carnet : l'établi le lit et ne le modifie pas")
	}
}
