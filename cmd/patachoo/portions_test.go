package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// --- Le choix des portions sur la fiche (PATA-138) -------------------------

// ficheAuxPortions demande la fiche avec le paramètre portions tel quel : les
// tests d'entrée invalide doivent pouvoir envoyer n'importe quoi.
func ficheAuxPortions(mux http.Handler, cookie *http.Cookie, id, portions string) *httptest.ResponseRecorder {
	return avecCookie(mux, http.MethodGet, "/recettes/"+id+"?portions="+url.QueryEscape(portions), cookie)
}

// recetteDeFarine pose une recette à ces portions, avec une seule ligne
// analysée « quantité g farine ».
func recetteDeFarine(t *testing.T, app core.App, servings int, quantite float64) *core.Record {
	t.Helper()

	recette := recetteEnBase(t, app, map[string]any{"servings": servings})
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "farine",
		"position": 1,
		"quantity": quantite,
		"unit":     "g",
		"food":     "farine",
	})
	return recette
}

// premiereLigneAffichee rend le texte affiché de la première ligne
// d'ingrédient, sans l'attribut title qui porte la ligne brute.
func premiereLigneAffichee(t *testing.T, reponse *httptest.ResponseRecorder) string {
	t.Helper()

	if reponse.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu 200", reponse.Code)
	}
	return contenu(ingredientsRendus(t, reponse.Body.String())[0])
}

var formulaireRendu = regexp.MustCompile(`(?s)<form[^>]*>.*?</form>`)

// formulaireDesPortions rend le formulaire qui porte le champ portions, ou ""
// s'il n'y en a pas.
func formulaireDesPortions(corps string) string {
	for _, formulaire := range formulaireRendu.FindAllString(corps, -1) {
		if strings.Contains(formulaire, `name="portions"`) {
			return formulaire
		}
	}
	return ""
}

func TestLesPortionsDemandeesRecalculentLesQuantites(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 4, 200)

	for portions, attendu := range map[string]string{"8": "400 g ", "2": "100 g "} {
		ligne := premiereLigneAffichee(t, ficheAuxPortions(mux, cookie, recette.Id, portions))
		if !strings.HasPrefix(ligne, attendu) {
			t.Errorf("?portions=%s : ligne %q, attendu qu'elle commence par %q", portions, ligne, attendu)
		}
	}
}

// Deux décimales au plus, virgule française, sans zéro traînant.
func TestUneQuantiteRecalculeeSArronditADeuxDecimales(t *testing.T) {
	cas := []struct {
		nom      string
		servings int
		quantite float64
		portions string
		attendu  string
	}{
		{"exacte", 4, 100, "3", "75 g "},
		{"arrondie", 3, 200, "1", "66,67 g "},
		{"sans zéro traînant", 4, 1, "2", "0,5 g "},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux, cookie := serveurConnecte(t)
			recette := recetteDeFarine(t, app, c.servings, c.quantite)

			ligne := premiereLigneAffichee(t, ficheAuxPortions(mux, cookie, recette.Id, c.portions))
			if !strings.HasPrefix(ligne, c.attendu) {
				t.Errorf("ligne %q, attendu qu'elle commence par %q", ligne, c.attendu)
			}
		})
	}
}

// Une quantité que l'arrondi ramènerait à zéro ne disparaît pas : elle
// s'affiche au plus petit pas de l'arrondi.
func TestUneQuantiteRecalculeeNonNulleNeTombeJamaisAZero(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 100, 0.1)

	ligne := premiereLigneAffichee(t, ficheAuxPortions(mux, cookie, recette.Id, "1"))
	if !strings.HasPrefix(ligne, "0,01 g ") {
		t.Errorf("ligne %q, attendu qu'elle commence par « 0,01 g »", ligne)
	}
}

// Demander les portions de la recette ne change rien, pas même l'arrondi : une
// quantité à trois décimales s'affiche comme sans paramètre.
func TestDemanderLesPortionsDeLaRecetteNeChangeRien(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 4, 0.333)

	sans := premiereLigneAffichee(t, fiche(mux, cookie, recette.Id))
	avec := premiereLigneAffichee(t, ficheAuxPortions(mux, cookie, recette.Id, "4"))

	if avec != sans {
		t.Errorf("?portions=4 sur une recette à 4 : %q, attendu %q comme sans paramètre", avec, sans)
	}
	if !strings.HasPrefix(sans, "0,333 g ") {
		t.Errorf("sans paramètre : %q, attendu la quantité d'origine « 0,333 »", sans)
	}
}

func TestLeBandeauAfficheLesPortionsDemandees(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 4, 200)

	corps := ficheAuxPortions(mux, cookie, recette.Id, "8").Body.String()
	if !strings.Contains(corps, "<dt>Portions</dt><dd>8</dd>") {
		t.Errorf("bandeau sans « Portions 8 » :\n%s", corps)
	}
}

// Le pluriel suit la quantité recalculée : 1,5 reste au singulier en français.
func TestLAccordSuitLaQuantiteRecalculee(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteEnBase(t, app, map[string]any{"servings": 4})
	ligneEnBase(t, app, recette, map[string]any{
		"raw":      "3 œufs",
		"position": 1,
		"quantity": 3,
		"food":     "œuf",
	})

	reponse := ficheAuxPortions(mux, cookie, recette.Id, "2")
	ligne := ingredientsRendus(t, reponse.Body.String())[0]

	if !strings.HasPrefix(contenu(ligne), "1,5 ") {
		t.Errorf("ligne %q, attendu qu'elle commence par « 1,5 »", contenu(ligne))
	}
	if got := alimentRendu(t, ligne); got != "œuf" {
		t.Errorf("aliment rendu %q, attendu « œuf » au singulier : %q", got, ligne)
	}
}

// Une ligne sans quantité et une ligne non analysée n'ont rien à recalculer.
func TestLesLignesSansQuantiteOuNonAnalyseesRestentInchangees(t *testing.T) {
	cas := []struct {
		nom   string
		ligne map[string]any
	}{
		{"sans quantité", map[string]any{"raw": "une pincée de sel", "food": "sel"}},
		{"non analysée", map[string]any{"raw": "200 g de farine", "quantity": 200}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux, cookie := serveurConnecte(t)
			recette := recetteEnBase(t, app, map[string]any{"servings": 4})
			c.ligne["position"] = 1
			ligneEnBase(t, app, recette, c.ligne)

			sans := premiereLigneAffichee(t, fiche(mux, cookie, recette.Id))
			avec := premiereLigneAffichee(t, ficheAuxPortions(mux, cookie, recette.Id, "8"))

			if avec != sans {
				t.Errorf("?portions=8 : %q, attendu %q comme sans paramètre", avec, sans)
			}
		})
	}
}

func TestUneRecetteSansPortionsNOffreNiNAppliqueLeChoix(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 0, 200)

	reponse := ficheAuxPortions(mux, cookie, recette.Id, "8")
	if formulaire := formulaireDesPortions(reponse.Body.String()); formulaire != "" {
		t.Errorf("choix des portions offert sur une recette sans portions : %s", formulaire)
	}
	if ligne := premiereLigneAffichee(t, reponse); !strings.HasPrefix(ligne, "200 g ") {
		t.Errorf("?portions=8 appliqué sans portions de référence : %q", ligne)
	}
}

// Une valeur qu'on ne sait pas lire est ignorée, jamais une erreur. La borne
// est comprise : 100 est accepté, 101 ne l'est plus.
func TestUnParametrePortionsInvalideEstIgnore(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 4, 200)

	for _, portions := range []string{"", "abc", "0", "-2", "2.5", "2,5", "101", "99999999999999999999"} {
		reponse := ficheAuxPortions(mux, cookie, recette.Id, portions)
		if ligne := premiereLigneAffichee(t, reponse); !strings.HasPrefix(ligne, "200 g ") {
			t.Errorf("?portions=%q : ligne %q, attendu la quantité d'origine", portions, ligne)
		}
		if corps := reponse.Body.String(); !strings.Contains(corps, "<dt>Portions</dt><dd>4</dd>") {
			t.Errorf("?portions=%q : bandeau sans les portions de la recette", portions)
		}
	}

	if ligne := premiereLigneAffichee(t, ficheAuxPortions(mux, cookie, recette.Id, "100")); !strings.HasPrefix(ligne, "5000 g ") {
		t.Errorf("?portions=100 : ligne %q, attendu « 5000 g » à la borne", ligne)
	}
}

// Rien n'est écrit : ni en base, ni en cookie, et la consultation suivante
// repart des portions de la recette.
func TestChoisirLesPortionsNEcritRien(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 4, 200)

	temoin := fiche(mux, cookie, recette.Id)
	choisie := ficheAuxPortions(mux, cookie, recette.Id, "8")

	if got, attendu := nomsDesCookiesPoses(choisie), nomsDesCookiesPoses(temoin); got != attendu {
		t.Errorf("cookies posés avec ?portions=8 : %q, sans paramètre : %q", got, attendu)
	}

	relue, err := app.FindRecordById("recipes", recette.Id)
	if err != nil {
		t.Fatalf("relecture de la recette : %v", err)
	}
	if got := relue.GetInt("servings"); got != 4 {
		t.Errorf("servings en base = %d après consultation, attendu 4", got)
	}
	lignes, err := app.FindRecordsByFilter("ingredients", "recipe = {:r}", "", 0, 0, map[string]any{"r": recette.Id})
	if err != nil || len(lignes) != 1 {
		t.Fatalf("relecture des ingrédients : %v (%d lignes)", err, len(lignes))
	}
	if got := lignes[0].GetFloat("quantity"); got != 200 {
		t.Errorf("quantity en base = %v après consultation, attendu 200", got)
	}

	suivante := fiche(mux, cookie, recette.Id)
	if ligne := premiereLigneAffichee(t, suivante); !strings.HasPrefix(ligne, "200 g ") {
		t.Errorf("consultation suivante : %q, attendu la quantité d'origine", ligne)
	}
	if !strings.Contains(suivante.Body.String(), "<dt>Portions</dt><dd>4</dd>") {
		t.Errorf("consultation suivante sans les portions de la recette")
	}
}

// nomsDesCookiesPoses rend les noms des cookies que la réponse pose, triés.
func nomsDesCookiesPoses(reponse *httptest.ResponseRecorder) string {
	var noms []string
	for _, c := range reponse.Result().Cookies() {
		noms = append(noms, c.Name)
	}
	sort.Strings(noms)
	return strings.Join(noms, ",")
}

// Le choix passe par un formulaire GET vers la fiche elle-même : il marche
// sans JavaScript, et il garde la valeur affichée.
func TestLeChoixDesPortionsEstUnFormulaireGetVersLaFiche(t *testing.T) {
	app, mux, cookie := serveurConnecte(t)
	recette := recetteDeFarine(t, app, 4, 200)

	for portions, attendu := range map[string]string{"": `value="4"`, "8": `value="8"`} {
		var reponse *httptest.ResponseRecorder
		if portions == "" {
			reponse = fiche(mux, cookie, recette.Id)
		} else {
			reponse = ficheAuxPortions(mux, cookie, recette.Id, portions)
		}

		formulaire := formulaireDesPortions(reponse.Body.String())
		if formulaire == "" {
			t.Fatalf("?portions=%q : aucun formulaire de choix des portions", portions)
		}
		ouvrante := regexp.MustCompile(`<form[^>]*>`).FindString(formulaire)
		for _, attribut := range []string{`method="get"`, `action="/recettes/` + recette.Id + `"`} {
			if !strings.Contains(ouvrante, attribut) {
				t.Errorf("formulaire sans %s : %s", attribut, ouvrante)
			}
		}
		if !strings.Contains(formulaire, attendu) {
			t.Errorf("?portions=%q : le champ ne porte pas %s : %s", portions, attendu, formulaire)
		}
	}
}
