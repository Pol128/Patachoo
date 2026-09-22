package main

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// L'établi : la page détail, parcourue et cherchée.
//
// Ce qui est en cause ici n'est pas ce que l'ouvrier écrit — analyse_test.go le
// dit déjà — mais ce que la page ramène d'une table de formes déjà posée, et
// ce qu'elle en montre. Les formes sont donc écrites à la main, comme la vue
// agrégée les écrit : c'est le seul moyen de poser une lecture, des
// occurrences et une provenance choisies.

// --- Fixtures ---------------------------------------------------------------

// passeSurLInstance pose une passe terminée dont la source est la base de
// l'instance : c'est elle, et elle seule, qui donne une provenance à relire.
func passeSurLInstance(t *testing.T, app core.App, formes ...formeDeTest) *core.Record {
	t.Helper()

	passe := passeTermineeDeTest(t, app, formes...)
	passe.Set("source", sourceInstance)
	if err := app.Save(passe); err != nil {
		t.Fatalf("passe sur l'instance : %v", err)
	}
	return passe
}

// laForme relit la forme d'une passe par sa ligne brute : la fiche se demande
// par l'identifiant, que le test n'écrit pas lui-même.
func laForme(t *testing.T, app core.App, passe *core.Record, brut string) *core.Record {
	t.Helper()

	forme, err := app.FindFirstRecordByFilter("analyses_formes",
		"analysis = {:passe} && raw = {:brut}",
		dbx.Params{"passe": passe.Id, "brut": brut})
	if err != nil {
		t.Fatalf("forme %q de la passe : %v", brut, err)
	}
	return forme
}

// lue rend la lecture champ à champ qu'une forme de test porte.
func lue(quantite float64, unite, partitif, aliment, note string) *lectureDUneForme {
	return &lectureDUneForme{
		Quantite: &quantite,
		Unite:    unite,
		Partitif: partitif,
		Aliment:  aliment,
		Note:     note,
	}
}

// --- Lecture de la liste -----------------------------------------------------

// ligneDeForme reconnaît une ligne de la liste, et chacune de ses cellules.
//
// Une lecture textuelle et volontairement bête, comme celle de la vue agrégée :
// ce qui est en cause est ce que la cellule porte, pas la mise en forme. Les
// cellules sont lues par « [^<]* » à dessein — une ligne brute qui porterait du
// balisage non échappé ferait rougir ce motif-là avant tout le reste.
var ligneDeForme = regexp.MustCompile(
	`<tr class="forme"><td class="brut"><a href="([^"]*)">([^<]*)</a></td>` +
		`<td class="quantite">([^<]*)</td>` +
		`<td class="unite">([^<]*)</td>` +
		`<td class="partitif">([^<]*)</td>` +
		`<td class="aliment-lu">([^<]*)</td>` +
		`<td class="note">([^<]*)</td>` +
		`<td class="facultatif">([^<]*)</td>` +
		`<td class="rangement"><a href="([^"]*)">([^<]*)</a></td>` +
		`<td class="signaux">(.*?)</td>` +
		`<td class="occurrences">([^<]*)</td></tr>`)

// formeAffichee est ce qu'une ligne d'écran montre.
type formeAffichee struct {
	Lien         string
	Brut         string
	Quantite     string
	Unite        string
	Partitif     string
	Aliment      string
	Note         string
	Facultatif   string
	LienDuGroupe string
	Groupe       string
	Signaux      string
	Occurrences  string
}

// formesAffichees rend les lignes de la liste, dans l'ordre où elles sont
// écrites.
func formesAffichees(corps string) []formeAffichee {
	var lues []formeAffichee
	for _, ligne := range ligneDeForme.FindAllStringSubmatch(corps, -1) {
		lues = append(lues, formeAffichee{
			Lien:         ligne[1],
			Brut:         html.UnescapeString(ligne[2]),
			Quantite:     html.UnescapeString(ligne[3]),
			Unite:        html.UnescapeString(ligne[4]),
			Partitif:     html.UnescapeString(ligne[5]),
			Aliment:      html.UnescapeString(ligne[6]),
			Note:         html.UnescapeString(ligne[7]),
			Facultatif:   ligne[8],
			LienDuGroupe: html.UnescapeString(ligne[9]),
			Groupe:       html.UnescapeString(ligne[10]),
			Signaux:      html.UnescapeString(ligne[11]),
			Occurrences:  ligne[12],
		})
	}
	return lues
}

// brutsAffiches ne garde que la ligne brute de chaque forme, dans l'ordre.
func brutsAffiches(corps string) []string {
	var bruts []string
	for _, forme := range formesAffichees(corps) {
		bruts = append(bruts, forme.Brut)
	}
	return bruts
}

// laListeDesFormes demande la page avec la chaîne de requête donnée, et exige
// un 200 : rien ne se lit sur une page qui n'est pas rendue.
func laListeDesFormes(t *testing.T, mux http.Handler, cookie *http.Cookie, requete url.Values) string {
	t.Helper()

	rec := listeDesFormesBrute(mux, cookie, requete)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur la liste des formes, attendu %d — corps :\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// listeDesFormesBrute demande la même page sans rien exiger de son statut.
func listeDesFormesBrute(mux http.Handler, cookie *http.Cookie, requete url.Values) *httptest.ResponseRecorder {
	cible := cheminDesFormesDeLEtabli
	if len(requete) > 0 {
		cible += "?" + requete.Encode()
	}
	return avecCookie(mux, http.MethodGet, cible, cookie)
}

// laPageA demande une adresse déjà construite — celle qu'un lien de la page
// précédente porte. C'est ce qui fait qu'un va-et-vient se teste comme on le
// parcourt, plutôt qu'en recomposant l'URL attendue.
func laPageA(t *testing.T, mux http.Handler, cookie *http.Cookie, adresse string) string {
	t.Helper()

	rec := avecCookie(mux, http.MethodGet, adresse, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur %s, attendu %d — corps :\n%s",
			rec.Code, adresse, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// laFiche demande la fiche d'une forme et exige un 200.
func laFiche(t *testing.T, mux http.Handler, cookie *http.Cookie, forme *core.Record) string {
	t.Helper()
	return laPageA(t, mux, cookie, cheminDeLaFicheDUneForme(forme.Id))
}

// lienDeLAlimentCanonique reconnaît, dans la vue agrégée, le lien qui descend
// d'un groupe vers ses formes.
var lienDeLAlimentCanonique = regexp.MustCompile(`<td class="detail"><a href="([^"]*)">`)

// liensDesGroupes rend les adresses de détail de la vue agrégée, dans l'ordre
// de ses lignes.
func liensDesGroupes(corps string) []string {
	var liens []string
	for _, trouve := range lienDeLAlimentCanonique.FindAllStringSubmatch(corps, -1) {
		liens = append(liens, html.UnescapeString(trouve[1]))
	}
	return liens
}

// --- Les formes distinctes et leur compte ------------------------------------

// La liste montre les formes distinctes, pas les occurrences : une ligne brute
// présente trois fois dans le corpus produit une ligne à l'écran, comptée 3.
//
// C'est toute la raison d'être de la page : « 1 feuille de laurier » est mal
// lue identiquement sur ses milliers d'occurrences, et le verdict se pose une
// fois, sur la forme.
func TestLaListeMontreUneLigneParFormeAvecSonCompte(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeDeTest{
			brut: "1 feuille de laurier", aliment: "feuille de laurier", resolu: true,
			categorie: "Épices", occurrences: 3, signaux: []string{SignalMotsPerdus},
			lecture: lue(1, "feuille", "de", "laurier", ""),
		},
		formeResolue("2 oignons", "oignon", "Légumes", 1, SignalMotsPerdus),
	)

	corps := laListeDesFormes(t, mux, cookie, nil)

	lues := formesAffichees(corps)
	if len(lues) != 2 {
		t.Fatalf("%d ligne(s) affichée(s), attendu 2 — corps :\n%s", len(lues), corps)
	}
	if lues[0].Brut != "1 feuille de laurier" || lues[0].Occurrences != "3" {
		t.Errorf("première ligne %q comptée %q, attendu « 1 feuille de laurier » comptée 3",
			lues[0].Brut, lues[0].Occurrences)
	}
}

// La lecture du parser se lit champ à champ sur la ligne : c'est ce qu'on
// vient relire, et un aliment lu qui diffère de la ligne brute est exactement
// ce qu'on cherche à voir.
func TestChaqueLigneMontreLaLectureChampAChamp(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app, formeDeTest{
		brut: "20 cl de crème fraîche épaisse", aliment: "crème fraîche", resolu: true,
		categorie: "Crèmerie", occurrences: 12, signaux: []string{SignalMotsPerdus},
		lecture: lue(20, "cl", "de", "crème fraîche", "épaisse"),
	})

	lues := formesAffichees(laListeDesFormes(t, mux, cookie, nil))
	if len(lues) != 1 {
		t.Fatalf("%d ligne(s) affichée(s), attendu 1", len(lues))
	}

	ligne := lues[0]
	for nom, cellule := range map[string][2]string{
		"quantité":  {ligne.Quantite, "20"},
		"unité":     {ligne.Unite, "cl"},
		"partitif":  {ligne.Partitif, "de"},
		"aliment":   {ligne.Aliment, "crème fraîche"},
		"note":      {ligne.Note, "épaisse"},
		"rangement": {ligne.Groupe, "crème fraîche"},
	} {
		if cellule[0] != cellule[1] {
			t.Errorf("%s = %q, attendu %q", nom, cellule[0], cellule[1])
		}
	}
}

// --- Ce que la fiche dit en propre -------------------------------------------

// La fiche porte trois choses que la liste ne montre pas : le motif du parser
// qui a lu la ligne, le poids de la forme dans le corpus, et la cellule de
// catégorie — celle du lexique, ou la mention qui prend sa place quand rien
// n'a été reconnu.
//
// Le motif répond à « pourquoi cette lecture-là » : c'est la règle du moteur
// qui a attrapé la ligne, et la relire est la moitié du travail quand on
// cherche d'où sort un aliment bizarre. Le poids dit combien de fiches une
// lecture fausse abîme.
func TestLaFicheDitLeMotifLePoidsEtLaCategorieOuSonAbsence(t *testing.T) {
	const nonResolue = "1 pointe de couteau de garam masala"
	const resolue = "2 oignons"

	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeTermineeDeTest(t, app,
		formeDeTest{
			brut: nonResolue, aliment: "garam masala", occurrences: 37,
			motif: "quantite_unite_partitif", signaux: []string{SignalNonResolu},
			lecture: lue(1, "pointe de couteau", "de", "garam masala", ""),
		},
		formeDeTest{
			brut: resolue, aliment: "oignon", resolu: true, categorie: "Légumes",
			occurrences: 4, motif: "quantite_aliment",
			lecture: lue(2, "", "", "oignon", ""),
		})

	t.Run("non résolue", func(t *testing.T) {
		fiche := laFiche(t, mux, cookie, laForme(t, app, passe, nonResolue))

		for nom, attendu := range map[string]string{
			"le motif du parser":      `<dd class="motif">quantite_unite_partitif</dd>`,
			"le poids dans le corpus": "Vue 37 fois",
			"la non-résolution":       `<dd class="categorie">aliment non résolu par le lexique</dd>`,
		} {
			if !strings.Contains(fiche, attendu) {
				t.Errorf("la fiche ne porte pas %s (%q) — corps :\n%s", nom, attendu, fiche)
			}
		}
	})

	// La catégorie du lexique et la mention de non-résolution sont la même
	// cellule : sans ce second cas, retirer ce que « resolved » décide
	// laisserait la suite verte.
	t.Run("résolue", func(t *testing.T) {
		fiche := laFiche(t, mux, cookie, laForme(t, app, passe, resolue))

		if !strings.Contains(fiche, `<dd class="categorie">Légumes</dd>`) {
			t.Errorf("la fiche d'une forme résolue ne porte pas sa catégorie — corps :\n%s", fiche)
		}
		if strings.Contains(fiche, "aliment non résolu") {
			t.Errorf("la fiche d'une forme résolue la dit non résolue — corps :\n%s", fiche)
		}
	})
}

// --- Le va-et-vient entre le groupe et le détail -----------------------------

// Depuis le groupe, au détail : la vue agrégée porte, sur chaque groupe, le
// lien qui descend sur les formes qui l'ont produit.
//
// Le lien est suivi plutôt que recomposé : c'est le va-et-vient qu'on teste,
// et une adresse attendue écrite à la main ne dirait pas qu'elle est atteignable.
func TestDepuisLeGroupeOnDescendSurSesFormes(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("1 feuille de laurier", "feuille de laurier", "Épices", 400, SignalMotsPerdus),
		formeResolue("2 feuilles de laurier", "feuille de laurier", "Épices", 40, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus),
	)

	agregee := laVueAgregee(t, mux, cookie, nil)
	liens := liensDesGroupes(agregee)
	if len(liens) != 2 {
		t.Fatalf("%d lien(s) de détail dans la vue agrégée, attendu 2 — corps :\n%s", len(liens), agregee)
	}

	detail := laPageA(t, mux, cookie, liens[0])
	if bruts := brutsAffiches(detail); len(bruts) != 2 {
		t.Errorf("le détail du premier groupe montre %v, attendu ses deux formes — corps :\n%s",
			bruts, detail)
	}
}

// Depuis une forme, un lien remonte au groupe qui l'a attrapée : « rangée sous
// feuille de laurier, voir les 6 autres formes ». C'est ce va-et-vient qui fait
// qu'on comprend une erreur au lieu de la constater.
func TestDepuisUneFormeOnRemonteAuGroupeQuiLaContient(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeTermineeDeTest(t, app,
		formeResolue("1 feuille de laurier", "feuille de laurier", "Épices", 400, SignalMotsPerdus),
		formeResolue("2 feuilles de laurier", "feuille de laurier", "Épices", 40, SignalMotsPerdus),
		formeResolue("laurier", "feuille de laurier", "Épices", 4, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus),
	)

	fiche := laFiche(t, mux, cookie, laForme(t, app, passe, "laurier"))

	if !strings.Contains(fiche, "feuille de laurier") {
		t.Errorf("la fiche ne dit pas sous quel aliment la forme est rangée — corps :\n%s", fiche)
	}
	if !strings.Contains(fiche, "2 autres formes") {
		t.Errorf("la fiche ne dit pas combien d'autres formes le groupe porte — corps :\n%s", fiche)
	}

	remontee := regexp.MustCompile(`<p class="rangement">.*?<a href="([^"]*)"`).FindStringSubmatch(fiche)
	if remontee == nil {
		t.Fatalf("la fiche ne porte aucun lien vers le groupe — corps :\n%s", fiche)
	}

	groupe := laPageA(t, mux, cookie, html.UnescapeString(remontee[1]))
	if bruts := brutsAffiches(groupe); len(bruts) != 3 {
		t.Errorf("le groupe remonté montre %v, attendu les trois formes du laurier", bruts)
	}
}

// La page s'ouvre par son URL seule, sans être passé par l'agrégé : un lien
// partagé ou mis en favori désigne le groupe et rien d'autre.
func TestLaListeSOuvreParSonUrlSeuleFiltreeSurUnGroupe(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("1 feuille de laurier", "feuille de laurier", "Épices", 400, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus),
	)

	corps := laListeDesFormes(t, mux, cookie,
		url.Values{parametreDeLAliment: {"feuille de laurier"}})

	if bruts := brutsAffiches(corps); len(bruts) != 1 || bruts[0] != "1 feuille de laurier" {
		t.Errorf("la liste filtrée montre %v, attendu la seule forme du laurier", bruts)
	}
}

// Le groupe des lignes dont aucun aliment n'a été lu se désigne par une clé
// vide, et il est atteignable comme les autres : « aliment » posé mais vide
// filtre, son absence ne filtre pas.
//
// Sans cette distinction, le lien du groupe des aliments vides ouvrirait la
// liste entière — c'est-à-dire exactement la page dont on vient.
func TestLeGroupeDesAlimentsVidesEstAtteignableParUneCleVide(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeSansAliment("2 cuillères à soupe", 90),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus),
	)

	filtree := laListeDesFormes(t, mux, cookie, url.Values{parametreDeLAliment: {""}})
	if bruts := brutsAffiches(filtree); len(bruts) != 1 || bruts[0] != "2 cuillères à soupe" {
		t.Errorf("le groupe des aliments vides montre %v, attendu sa seule forme", bruts)
	}

	entiere := laListeDesFormes(t, mux, cookie, nil)
	if bruts := brutsAffiches(entiere); len(bruts) != 2 {
		t.Errorf("sans paramètre la liste montre %v, attendu les deux formes", bruts)
	}
}

// --- La recherche ------------------------------------------------------------

// L'index FTS5 de PATA-122 replie les accents et cherche par préfixe : c'est
// son tokeniseur qui le fait, pas le code Go, et c'est le comportement que la
// page promet.
func TestLaRechercheTrouveSansAccentsEtParPrefixe(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("20 cl de crème fraîche", "crème fraîche", "Crèmerie", 120, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus),
	)

	for nom, terme := range map[string]string{
		"sans accents": "creme",
		"par préfixe":  "crè",
		"les deux":     "crem",
	} {
		t.Run(nom, func(t *testing.T) {
			corps := laListeDesFormes(t, mux, cookie, url.Values{parametreDuTerme: {terme}})
			if bruts := brutsAffiches(corps); len(bruts) != 1 || bruts[0] != "20 cl de crème fraîche" {
				t.Errorf("« %s » ramène %v, attendu la seule forme de la crème", terme, bruts)
			}
		})
	}
}

// La recherche porte aussi sur l'aliment canonique, que la ligne brute ne
// contient pas forcément : c'est la seconde colonne de l'index, et c'est elle
// qui répond à « d'où sort cet aliment bizarre ? ».
func TestLaRechercheTrouveParLAlimentCanoniqueAbsentDeLaLigne(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("1 CS de maïzena", "fécule de maïs", "Épicerie", 60, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus),
	)

	corps := laListeDesFormes(t, mux, cookie, url.Values{parametreDuTerme: {"fecule"}})

	if bruts := brutsAffiches(corps); len(bruts) != 1 || bruts[0] != "1 CS de maïzena" {
		t.Errorf("la recherche par l'aliment canonique ramène %v, attendu la maïzena", bruts)
	}
}

// La syntaxe de requête de FTS5 est un langage : un terme portant un
// guillemet, « * », « - » ou « NEAR » ne doit ni faire échouer la requête ni
// changer son sens. Il est cherché comme du texte.
func TestUnTermeDeSyntaxeFTS5EstCherchéCommeDuTexte(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 300, SignalMotsPerdus),
		formeResolue("1 carotte", "carotte", "Légumes", 30, SignalMotsPerdus),
	)

	for nom, cas := range map[string]struct {
		terme   string
		attendu []string
	}{
		// Une négation FTS5 : sans citation, elle retirerait l'oignon.
		"tiret":      {"-oignon", []string{"2 oignons"}},
		"astérisque": {"oign*", []string{"2 oignons"}},
		"guillemet":  {`oignon"`, []string{"2 oignons"}},
		// Un opérateur de proximité : cité, ce n'est qu'un mot qu'aucune
		// forme ne porte.
		"NEAR": {"NEAR", nil},
		// Deux mots juxtaposés restent un ET implicite, et aucune forme ne
		// porte les deux.
		"deux mots": {"oignon carotte", nil},
	} {
		t.Run(nom, func(t *testing.T) {
			corps := laListeDesFormes(t, mux, cookie, url.Values{parametreDuTerme: {cas.terme}})
			if bruts := brutsAffiches(corps); strings.Join(bruts, "|") != strings.Join(cas.attendu, "|") {
				t.Errorf("« %s » ramène %v, attendu %v", cas.terme, bruts, cas.attendu)
			}
		})
	}
}

// Un terme qui ne ramène rien le dit, et ne passe pas pour une page cassée.
func TestUneRechercheSansResultatLeDit(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 300, SignalMotsPerdus))

	corps := laListeDesFormes(t, mux, cookie, url.Values{parametreDuTerme: {"salsifis"}})

	if bruts := brutsAffiches(corps); len(bruts) != 0 {
		t.Errorf("la recherche ramène %v, attendu rien", bruts)
	}
	if !strings.Contains(corps, `class="absence"`) {
		t.Errorf("la page ne dit pas que la recherche n'a rien ramené — corps :\n%s", corps)
	}
}

// formulaireDeRecherche reconnaît le formulaire de la liste : son action, et
// tout ce qu'il contient.
var formulaireDeRecherche = regexp.MustCompile(
	`(?s)<form class="recherche" method="get" action="([^"]*)"[^>]*>(.*?)</form>`)

// champCacheDuFormulaire reconnaît un champ caché, tel que le gabarit l'écrit.
var champCacheDuFormulaire = regexp.MustCompile(
	`<input type="hidden" name="([^"]*)" value="([^"]*)">`)

// rechercheDepuis rejoue la recherche telle que la page la soumettrait :
// l'action et les champs cachés sont relus dans le corps rendu, et le terme
// s'y ajoute.
//
// Relus, et non recomposés : un champ caché retiré du gabarit disparaît alors
// de la requête, exactement comme il disparaîtrait d'un navigateur — ce qui
// est le seul moyen de faire dire à un test que ces champs servent à quelque
// chose.
func rechercheDepuis(t *testing.T, corps, terme string) string {
	t.Helper()

	trouve := formulaireDeRecherche.FindStringSubmatch(corps)
	if trouve == nil {
		t.Fatalf("aucun formulaire de recherche dans la page — corps :\n%s", corps)
	}

	valeurs := url.Values{}
	for _, champ := range champCacheDuFormulaire.FindAllStringSubmatch(trouve[2], -1) {
		valeurs.Set(html.UnescapeString(champ[1]), html.UnescapeString(champ[2]))
	}
	valeurs.Set(parametreDuTerme, terme)

	return html.UnescapeString(trouve[1]) + "?" + valeurs.Encode()
}

// Chercher depuis un groupe cherche dans le groupe, et dans la passe d'où l'on
// vient : le filtre et l'analyse partent avec le terme.
//
// C'est le parcours central de la page — descendre d'un groupe, puis chercher
// dedans — et il se joue par le formulaire tel que la page l'écrit. Les deux
// jeux posés ici sont ceux qui apparaîtraient si l'un des deux critères se
// perdait en route : une autre forme de la même passe si le filtre tombe, une
// forme de la passe la plus récente si l'analyse tombe. Dans les deux cas le
// résultat serait plausible, et c'est pourquoi il faut un test.
func TestChercherDepuisUnGroupeGardeLeFiltreEtLaPasse(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	ancienne := passeTermineeDeTest(t, app,
		formeResolue("1 feuille de laurier", "feuille de laurier", "Épices", 400, SignalMotsPerdus),
		formeResolue("2 feuilles de laurier", "feuille de laurier", "Épices", 40, SignalMotsPerdus),
		formeResolue("laurier moulu", "poudre de laurier", "Épices", 4, SignalMotsPerdus))
	recule(t, app, ancienne, "2026-09-18 10:00:00.000Z")

	passeTermineeDeTest(t, app,
		formeResolue("3 feuilles de laurier", "feuille de laurier", "Épices", 7, SignalMotsPerdus))

	groupe := laPageA(t, mux, cookie, lienDesFormesDuGroupe(ancienne.Id, "feuille de laurier"))
	cherchee := laPageA(t, mux, cookie, rechercheDepuis(t, groupe, "laurier"))

	attendu := []string{"1 feuille de laurier", "2 feuilles de laurier"}
	if bruts := brutsAffiches(cherchee); strings.Join(bruts, "|") != strings.Join(attendu, "|") {
		t.Errorf("chercher « laurier » depuis le groupe ramène %v, attendu %v — corps :\n%s",
			bruts, attendu, cherchee)
	}
}

// --- Le bandeau de bascule ----------------------------------------------------

// bandeauDeBascule reconnaît, dans le bandeau d'un groupe filtré, l'adresse
// qui remonte à toutes les formes de la passe.
var bandeauDeBascule = regexp.MustCompile(`(?s)<p class="bascule">.*?<a href="([^"]*)">`)

// lienDeLaBascule rend cette adresse, telle que la page l'écrit — elle se suit
// comme un lien, elle ne se recompose pas.
func lienDeLaBascule(t *testing.T, corps string) string {
	t.Helper()

	trouve := bandeauDeBascule.FindStringSubmatch(corps)
	if trouve == nil {
		t.Fatalf("aucun bandeau de bascule dans la page — corps :\n%s", corps)
	}
	return html.UnescapeString(trouve[1])
}

// « Toutes les formes de la passe » retire le groupe, et ne retire que lui :
// la passe et le terme partent avec.
//
// C'est la seule ligne de logique du bandeau, et trois façons de la casser
// passeraient autrement en silence — la supprimer, y laisser le filtre, ou
// oublier l'analyse. Chacune rendrait une page plausible : celle du groupe
// qu'on voulait justement quitter, ou celle de la passe la plus récente. Les
// formes posées ici sont celles qui trahissent chaque étourderie.
func TestLaBasculeRetireLeGroupeEtLuiSeul(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	ancienne := passeTermineeDeTest(t, app,
		formeResolue("1 feuille de laurier", "feuille de laurier", "Épices", 400, SignalMotsPerdus),
		formeResolue("laurier moulu", "poudre de laurier", "Épices", 40, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 4, SignalMotsPerdus))
	recule(t, app, ancienne, "2026-09-18 10:00:00.000Z")

	passeTermineeDeTest(t, app,
		formeResolue("3 feuilles de laurier", "feuille de laurier", "Épices", 7, SignalMotsPerdus))

	filtree := laListeDesFormes(t, mux, cookie, url.Values{
		parametreDeLAnalyse: {ancienne.Id},
		parametreDeLAliment: {"feuille de laurier"},
		parametreDuTerme:    {"laurier"},
	})
	toutes := laPageA(t, mux, cookie, lienDeLaBascule(t, filtree))

	// Les deux groupes de l'ancienne passe qui parlent de laurier, et eux
	// seuls : « laurier moulu » dit que le groupe est bien tombé, les oignons
	// diraient que le terme est tombé avec lui, et « 3 feuilles de laurier »
	// que l'analyse ne l'a pas suivi.
	attendu := []string{"1 feuille de laurier", "laurier moulu"}
	if bruts := brutsAffiches(toutes); strings.Join(bruts, "|") != strings.Join(attendu, "|") {
		t.Errorf("la bascule ramène %v, attendu %v — corps :\n%s", bruts, attendu, toutes)
	}

	// Et le bandeau a disparu : il annonce un groupe, sa survie dirait qu'un
	// groupe filtre encore.
	if strings.Contains(toutes, `class="bascule"`) {
		t.Errorf("le bandeau de bascule survit à la bascule — corps :\n%s", toutes)
	}
}

// --- L'échappement — le point de sécurité du lot -----------------------------

// Le corpus est du texte étranger affiché dans une page, et c'est tout l'objet
// de l'établi. Une ligne brute contenant du balisage est affichée
// littéralement, et n'exécute rien — sur la liste comme sur la fiche, et sur
// les champs lus comme sur le brut.
func TestUneLigneBruteContenantDuBalisageEstAfficheeLitteralement(t *testing.T) {
	const brut = `2 <script>alert("xss")</script> oignons`
	const note = `<img src=x onerror="alert(1)">`

	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeTermineeDeTest(t, app, formeDeTest{
		brut: brut, aliment: `<b>oignon</b>`, resolu: true,
		categorie: "Légumes", occurrences: 2, signaux: []string{SignalMotsPerdus},
		lecture: lue(2, `<i>u</i>`, "de", `<b>oignon</b>`, note),
	})

	for nom, corps := range map[string]string{
		"liste": laListeDesFormes(t, mux, cookie, nil),
		"fiche": laFiche(t, mux, cookie, laForme(t, app, passe, brut)),
	} {
		t.Run(nom, func(t *testing.T) {
			// Les fragments sont ceux de la charge, et non « <script> » nu :
			// la mise en page porte sa propre balise de script, et un motif
			// trop large rougirait sur elle plutôt que sur le corpus.
			for _, balise := range []string{
				`<script>alert(`, `alert("xss")</script>`,
				`<img src=x onerror=`, "<b>oignon</b>", "<i>u</i>",
			} {
				if strings.Contains(corps, balise) {
					t.Errorf("la page rend %q littéralement — corps :\n%s", balise, corps)
				}
			}
			// Et le texte est bien là, échappé : une page qui l'aurait
			// simplement perdu passerait le contrôle ci-dessus.
			if !strings.Contains(corps, html.EscapeString(brut)) {
				t.Errorf("la ligne brute échappée est absente de la page — corps :\n%s", corps)
			}
		})
	}
}

// Le corpus n'est pas la seule entrée étrangère de la page : le terme et la
// clé du groupe viennent de la chaîne de requête, donc de n'importe qui, et la
// page les réaffiche à quatre endroits. Le carnet porte déjà le même garde-fou
// sur sa propre recherche — TestLeChampDeRechercheEchappeLeTerme et
// TestLeMessageDAbsenceEchappeLeTerme, dans recettes_test.go.
//
// Rien n'est ouvert aujourd'hui : html/template échappe les quatre. C'est le
// test qui manquait, pas la protection — et il rougirait le jour où l'un des
// quatre passerait en template.HTML.
func TestLesParametresReflechisDeLaListeSontEchappes(t *testing.T) {
	const terme = `<script>alert("xss")</script>`
	const aliment = `"><script>alert('groupe')</script>`

	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus))

	// Une clé de groupe que rien ne porte et un terme que rien ne contient :
	// la page rend donc ses quatre réflexions d'un coup — les deux champs du
	// formulaire, le bandeau du groupe et le message d'absence.
	corps := laListeDesFormes(t, mux, cookie, url.Values{
		parametreDeLAliment: {aliment},
		parametreDuTerme:    {terme},
	})

	// Les fragments sont ceux des charges, et non « <script> » nu : la mise en
	// page porte sa propre balise de script.
	exigeSansAucun(t, corps, terme, aliment,
		`<script>alert(`, `alert("xss")</script>`, `alert('groupe')</script>`)

	for nom, cas := range map[string]struct {
		extrait string
		charge  string
	}{
		"le champ de recherche":    {entreBalises(corps, `name="q" value="`, `"`), terme},
		"le champ caché du groupe": {entreBalises(corps, `name="aliment" value="`, `"`), aliment},
		"le bandeau de bascule":    {entreBalises(corps, `<p class="bascule">`, "</p>"), aliment},
		"le message d'absence":     {entreBalises(corps, `<p class="absence">`, "</p>"), terme},
	} {
		t.Run(nom, func(t *testing.T) {
			if cas.extrait == "" {
				t.Fatalf("%s est absent de la page — corps :\n%s", nom, corps)
			}
			// Échappée, et présente : une page qui aurait simplement perdu la
			// valeur passerait le contrôle ci-dessus sans rien protéger.
			if !strings.Contains(cas.extrait, html.EscapeString(cas.charge)) {
				t.Errorf("la charge n'est pas rendue échappée dans %s : %q", nom, cas.extrait)
			}
		})
	}
}

// --- La provenance -----------------------------------------------------------

// provenanceAffichee reconnaît les recettes listées par la fiche.
var provenanceAffichee = regexp.MustCompile(`<li class="origine"><a href="([^"]*)">([^<]*)</a></li>`)

// origines rend les recettes de la provenance : leur adresse et leur titre.
func origines(corps string) [][2]string {
	var lues [][2]string
	for _, trouve := range provenanceAffichee.FindAllStringSubmatch(corps, -1) {
		lues = append(lues, [2]string{html.UnescapeString(trouve[1]), html.UnescapeString(trouve[2])})
	}
	return lues
}

// Sur un corpus venu de la base de l'instance, la provenance se relit à la
// demande : égalité sur ingredients.raw, puis la relation vers la fiche.
// Chaque entrée mène à sa recette.
func TestLaProvenanceListeLesRecettesEtChacuneMeneASaFiche(t *testing.T) {
	const brut = "1 feuille de laurier"

	app, mux, cookie := atelierDeLEtabli(t)
	creeRecette(t, app, recetteVoulue{titre: "Blanquette", ingredients: []string{brut, "2 oignons"}})
	creeRecette(t, app, recetteVoulue{titre: "Pot-au-feu", ingredients: []string{brut}})
	creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes", ingredients: []string{"3 pommes"}})

	passe := passeSurLInstance(t, app,
		formeResolue(brut, "feuille de laurier", "Épices", 2, SignalMotsPerdus))

	fiche := laFiche(t, mux, cookie, laForme(t, app, passe, brut))

	lues := origines(fiche)
	if len(lues) != 2 {
		t.Fatalf("%d recette(s) en provenance, attendu 2 — corps :\n%s", len(lues), fiche)
	}
	for _, origine := range lues {
		if origine[1] == "Tarte aux pommes" {
			t.Errorf("une recette qui ne porte pas la ligne est listée en provenance")
		}
		corps := laPageA(t, mux, cookie, origine[0])
		if !strings.Contains(corps, html.EscapeString(origine[1])) {
			t.Errorf("le lien %q ne mène pas à la fiche de %q", origine[0], origine[1])
		}
	}
}

// Une même recette qui porte deux fois la ligne n'est listée qu'une fois : la
// provenance nomme des recettes, pas des occurrences.
func TestUneRecetteQuiPorteDeuxFoisLaLigneNEstListeeQuUneFois(t *testing.T) {
	const brut = "1 pincée de sel"

	app, mux, cookie := atelierDeLEtabli(t)
	creeRecette(t, app, recetteVoulue{titre: "Blanquette", ingredients: []string{brut, "2 oignons", brut}})

	passe := passeSurLInstance(t, app,
		formeResolue(brut, "sel", "Épices", 2, SignalMotsPerdus))

	fiche := laFiche(t, mux, cookie, laForme(t, app, passe, brut))

	if lues := origines(fiche); len(lues) != 1 {
		t.Errorf("%d entrée(s) de provenance, attendu 1 — corps :\n%s", len(lues), fiche)
	}
}

// Un corpus fourni n'a aucune provenance — pas même un numéro de ligne, le
// fichier n'étant pas conservé. La fiche n'affiche alors ni provenance ni
// lien, plutôt qu'un lien mort.
//
// La ligne existe pourtant à l'identique dans la base de l'instance : c'est la
// source de la passe, et elle seule, qui décide.
func TestUneFormeSansProvenanceNAfficheNiProvenanceNiLien(t *testing.T) {
	const brut = "1 feuille de laurier"

	app, mux, cookie := atelierDeLEtabli(t)
	creeRecette(t, app, recetteVoulue{titre: "Blanquette", ingredients: []string{brut}})

	passe := passeTermineeDeTest(t, app,
		formeResolue(brut, "feuille de laurier", "Épices", 1, SignalMotsPerdus))
	if passe.GetString("source") != sourceFournie {
		t.Fatalf("la fixture ne pose pas un corpus fourni : %q", passe.GetString("source"))
	}

	fiche := laFiche(t, mux, cookie, laForme(t, app, passe, brut))

	if lues := origines(fiche); len(lues) != 0 {
		t.Errorf("%d entrée(s) de provenance sur un corpus fourni, attendu 0 — corps :\n%s", len(lues), fiche)
	}
	// Le bloc entier est absent, et pas seulement vide : une section
	// « d'où elle vient » suivie de rien se lit comme une panne.
	if strings.Contains(fiche, `class="provenance"`) || strings.Contains(fiche, `class="sans-provenance"`) {
		t.Errorf("la fiche d'un corpus fourni porte un bloc de provenance — corps :\n%s", fiche)
	}
}

// --- Le coût de la provenance ------------------------------------------------

// requetesSurLesIngredients reconnaît une requête émise sur la collection des
// ingrédients : c'est celle de la provenance, et la seule.
//
// Sur le FROM et non sur le seul nom : dbx journalise les requêtes paramètres
// substitués, et un identifiant de collection passé en valeur porterait le mot
// sans être une lecture de la table.
var requetesSurLesIngredients = regexp.MustCompile("(?i)FROM\\s+[`\"\\[]?ingredients")

// compteurDeRequetes compte les requêtes de provenance émises pendant un
// appel. Un verrou, parce que le journal de dbx est appelé depuis la goroutine
// qui exécute la requête.
type compteurDeRequetes struct {
	mu     sync.Mutex
	compte int
}

// compteLesRequetesDeProvenance branche le compteur sur la base, pour la durée
// du test.
func compteLesRequetesDeProvenance(t *testing.T, app core.App) *compteurDeRequetes {
	t.Helper()

	base, ok := app.ConcurrentDB().(*dbx.DB)
	if !ok {
		t.Fatalf("la base de test n'est pas un *dbx.DB : %T", app.ConcurrentDB())
	}

	compteur := &compteurDeRequetes{}
	base.QueryLogFunc = func(_ context.Context, _ time.Duration, sql string, _ *sql.Rows, _ error) {
		if !requetesSurLesIngredients.MatchString(sql) {
			return
		}
		compteur.mu.Lock()
		compteur.compte++
		compteur.mu.Unlock()
	}
	t.Cleanup(func() { base.QueryLogFunc = nil })
	return compteur
}

// remiseAZero rend le compte et le remet à zéro.
func (c *compteurDeRequetes) remiseAZero() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	compte := c.compte
	c.compte = 0
	return compte
}

// La liste des formes et le résultat de recherche n'émettent aucune requête de
// provenance : ingredients.raw n'est pas indexé, et une requête par ligne de
// liste coûterait une lecture complète de la table par forme affichée.
//
// Elle n'est émise que pour la forme affichée, c'est-à-dire sur sa fiche.
func TestLaProvenanceNEstEmiseQueSurLaFicheDUneForme(t *testing.T) {
	const brut = "1 feuille de laurier"

	app, mux, cookie := atelierDeLEtabli(t)
	creeRecette(t, app, recetteVoulue{titre: "Blanquette", ingredients: []string{brut}})
	passe := passeSurLInstance(t, app,
		formeResolue(brut, "feuille de laurier", "Épices", 1, SignalMotsPerdus),
		formeResolue("2 oignons", "oignon", "Légumes", 30, SignalMotsPerdus))

	compteur := compteLesRequetesDeProvenance(t, app)

	laListeDesFormes(t, mux, cookie, nil)
	if compte := compteur.remiseAZero(); compte != 0 {
		t.Errorf("%d requête(s) de provenance sur la liste, attendu 0", compte)
	}

	laListeDesFormes(t, mux, cookie, url.Values{parametreDuTerme: {"laurier"}})
	if compte := compteur.remiseAZero(); compte != 0 {
		t.Errorf("%d requête(s) de provenance sur une recherche, attendu 0", compte)
	}

	laFiche(t, mux, cookie, laForme(t, app, passe, brut))
	if compte := compteur.remiseAZero(); compte != 1 {
		t.Errorf("%d requête(s) de provenance sur la fiche, attendu 1", compte)
	}
}

// Le résultat de la provenance est borné : une ligne banale — « 1 pincée de
// sel » — est portée par tout le carnet, et la fiche ne doit pas en déplier
// des milliers.
func TestLaProvenanceEstBorneeEtLeDit(t *testing.T) {
	const brut = "1 pincée de sel"

	app, mux, cookie := atelierDeLEtabli(t)
	for i := range provenancesAffichees + 3 {
		creeRecette(t, app, recetteVoulue{
			titre:       fmt.Sprintf("Recette %02d", i),
			ingredients: []string{brut},
		})
	}

	passe := passeSurLInstance(t, app,
		formeResolue(brut, "sel", "Épices", provenancesAffichees+3, SignalMotsPerdus))

	fiche := laFiche(t, mux, cookie, laForme(t, app, passe, brut))

	if lues := origines(fiche); len(lues) != provenancesAffichees {
		t.Errorf("%d entrée(s) de provenance, attendu la borne de %d — corps :\n%s",
			len(lues), provenancesAffichees, fiche)
	}
	if !strings.Contains(fiche, `class="bornee"`) {
		t.Errorf("la fiche ne dit pas que la provenance est tronquée — corps :\n%s", fiche)
	}
}

// --- Pagination et paramètres malmenés ---------------------------------------

// La liste des formes d'un gros groupe ne tient pas sur un écran : elle se
// pagine comme la liste des recettes, et un paramètre malmené retombe sur la
// première page plutôt que de produire une erreur.
func TestLaListeSePagineEtUnParametreMalmeneRetombeSurLaPremierePage(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	formes := make([]formeDeTest, 0, parPage+3)
	for i := range parPage + 3 {
		formes = append(formes, formeResolue(
			fmt.Sprintf("%02d oignons", i), "oignon", "Légumes", parPage+3-i, SignalMotsPerdus))
	}
	passeTermineeDeTest(t, app, formes...)

	premiere := laListeDesFormes(t, mux, cookie, nil)
	if lues := brutsAffiches(premiere); len(lues) != parPage {
		t.Fatalf("%d forme(s) sur la première page, attendu %d", len(lues), parPage)
	}
	if !strings.Contains(premiere, `rel="next"`) {
		t.Error("la première page n'annonce pas de suivante alors que le jeu déborde")
	}

	seconde := laListeDesFormes(t, mux, cookie, url.Values{parametreDeLaPage: {"2"}})
	if lues := brutsAffiches(seconde); len(lues) != 3 {
		t.Errorf("%d forme(s) sur la seconde page, attendu 3", len(lues))
	}
	if !strings.Contains(seconde, `rel="prev"`) {
		t.Error("la seconde page n'annonce pas de précédente")
	}

	for nom, valeur := range map[string]string{
		"vide":        "",
		"non chiffré": "sel",
		"négatif":     "-3",
		"démesuré":    strconv.Itoa(pageMax * 4),
	} {
		t.Run(nom, func(t *testing.T) {
			rec := listeDesFormesBrute(mux, cookie, url.Values{parametreDeLaPage: {valeur}})
			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d sur ?page=%q, attendu %d", rec.Code, valeur, http.StatusOK)
			}
		})
	}
}

// Tant qu'aucune analyse n'est terminée, la page le dit et renvoie vers le
// lancement au lieu d'afficher une liste vide.
func TestSansAucuneAnalyseTermineeLaListeRenvoieAuLancement(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeDeTest(t, app, statutEnCours,
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus))

	corps := laListeDesFormes(t, mux, cookie, nil)

	if bruts := brutsAffiches(corps); len(bruts) != 0 {
		t.Errorf("%v affiché(s) sans analyse terminée, attendu rien", bruts)
	}
	if !strings.Contains(corps, `href="`+cheminDeLEtabli+`"`) {
		t.Errorf("la page ne renvoie pas vers le lancement — corps :\n%s", corps)
	}
}

// Une fiche demandée sur un identifiant qui ne désigne rien rend une page
// lisible, et non une 500 ni une page vide.
func TestUneFicheIntrouvableRendUnePageEtNonUneErreur(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	rec := avecCookie(mux, http.MethodGet, cheminDeLaFicheDUneForme("cettefo4meabs"), cookie)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("statut %d sur une fiche inconnue, attendu %d — corps :\n%s",
			rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `href="`+cheminDesFormesDeLEtabli+`"`) {
		t.Errorf("la page d'absence ne ramène pas à la liste — corps :\n%s", rec.Body.String())
	}
}

// --- Le droit d'entrer -------------------------------------------------------

// Les deux écrans sont réservés aux comptes portant le droit d'accès à
// l'établi, et le refus se teste dans le sens du refus : un compte authentifié
// sans le droit est refusé, un visiteur est renvoyé se connecter.
func TestLesFormesSontReserveesAuxCurateurs(t *testing.T) {
	app, mux := serveurDeLEtabli(t)
	compteParDefaut(t, app)
	ordinaire := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

	for nom, chemin := range map[string]string{
		"la liste": cheminDesFormesDeLEtabli,
		"la fiche": cheminDeLaFicheDUneForme("cettefo4meabs"),
	} {
		for qui, attendu := range map[string]struct {
			cookie *http.Cookie
			statut int
		}{
			"visiteur":         {nil, http.StatusSeeOther},
			"compte ordinaire": {ordinaire, http.StatusForbidden},
		} {
			t.Run(nom+" — "+qui, func(t *testing.T) {
				rec := avecCookie(mux, http.MethodGet, chemin, attendu.cookie)
				if rec.Code != attendu.statut {
					t.Fatalf("statut %d, attendu %d — corps :\n%s",
						rec.Code, attendu.statut, rec.Body.String())
				}
			})
		}
	}
}

// La tâche n'ajoute aucune route en écriture : l'établi ne modifie ni recipes
// ni ingredients, et les deux écrans ne répondent qu'au GET.
func TestLesFormesNAjoutentAucuneRouteEnEcriture(t *testing.T) {
	const brut = "1 feuille de laurier"

	app, mux, cookie := atelierDeLEtabli(t)
	creeRecette(t, app, recetteVoulue{titre: "Blanquette", ingredients: []string{brut}})
	passe := passeSurLInstance(t, app,
		formeResolue(brut, "feuille de laurier", "Épices", 1, SignalMotsPerdus))
	forme := laForme(t, app, passe, brut)

	avant := empreinteDes(t, app, "recipes", "ingredients", "analyses_formes")

	// Un 404 et non un 405 : aucun motif n'est enregistré pour ce couple
	// méthode/chemin, et c'est le gestionnaire d'absence de PocketBase qui
	// répond. Ce qui est en cause est qu'aucun gestionnaire ne soit atteint.
	for _, chemin := range []string{cheminDesFormesDeLEtabli, cheminDeLaFicheDUneForme(forme.Id)} {
		rec := avecCookie(mux, http.MethodPost, chemin, cookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("statut %d sur un POST vers %s, attendu %d — corps :\n%s",
				rec.Code, chemin, http.StatusNotFound, rec.Body.String())
		}
	}

	laListeDesFormes(t, mux, cookie, nil)
	laFiche(t, mux, cookie, forme)
	if apres := empreinteDes(t, app, "recipes", "ingredients", "analyses_formes"); apres != avant {
		t.Error("les écrans de détail ont touché au carnet : l'établi le lit et ne le modifie pas")
	}
}
