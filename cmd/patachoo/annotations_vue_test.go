package main

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// L'annotation, vue de l'écran : le fragment qui la dépose, ce qu'elle
// enregistre, et ce qui la garde.
//
// Ce qui est en cause ici n'est pas la fonction pure — annotations_test.go le
// dit déjà — mais ce que la route écrit, ce qu'elle refuse, et ce que les deux
// écrans en montrent.

// --- Fixtures ----------------------------------------------------------------

// passeSignee pose une passe terminée qui porte son empreinte : la version du
// moteur et la taille du lexique qui ont servi. C'est elle que l'annotation
// recopie.
func passeSignee(t *testing.T, app core.App, version string, entrees int, formes ...formeDeTest) *core.Record {
	t.Helper()

	passe := passeTermineeDeTest(t, app, formes...)
	passe.Set("engine_version", version)
	passe.Set("lexicon_entries", entrees)
	if err := app.Save(passe); err != nil {
		t.Fatalf("signature de la passe : %v", err)
	}
	return passe
}

// annoteLeGroupe poste une annotation de groupe et exige qu'elle passe.
func annoteLeGroupe(t *testing.T, mux http.Handler, cookie *http.Cookie,
	passe *core.Record, aliment string, champs url.Values) string {
	t.Helper()

	champs.Set(parametreDeLAnalyse, passe.Id)
	champs.Set(champDeLaCible, cibleDuGroupe)
	champs.Set(parametreDeLAliment, aliment)
	return corpsDUneAnnotationAcceptee(t, soumetUneAnnotation(mux, cookie, champs))
}

// annoteLaForme poste une annotation de forme et exige qu'elle passe.
func annoteLaForme(t *testing.T, mux http.Handler, cookie *http.Cookie,
	passe *core.Record, brut string, champs url.Values) string {
	t.Helper()

	champs.Set(parametreDeLAnalyse, passe.Id)
	champs.Set(champDeLaCible, cibleDeLaForme)
	champs.Set(parametreDeLaForme, brut)
	return corpsDUneAnnotationAcceptee(t, soumetUneAnnotation(mux, cookie, champs))
}

func corpsDUneAnnotationAcceptee(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur le dépôt d'une annotation, attendu %d — corps :\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// soumetUneAnnotation poste le formulaire d'annotation, muni de la paire
// anti-rejeu et de l'en-tête que HTMX pose.
func soumetUneAnnotation(mux http.Handler, cookie *http.Cookie, champs url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, cheminDeLAnnotation,
		strings.NewReader(leJetonEstPose(champs).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookieDuJetonDeTest())
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// lesAnnotations relit ce que la base porte, dans l'ordre d'écriture.
func lesAnnotations(t *testing.T, app core.App) []*core.Record {
	t.Helper()

	annotations, err := app.FindRecordsByFilter("analyses_annotations", "id != ''", "created", 0, 0)
	if err != nil {
		t.Fatalf("relecture des annotations : %v", err)
	}
	return annotations
}

// laSeuleAnnotation exige qu'il n'y en ait qu'une, et la rend.
func laSeuleAnnotation(t *testing.T, app core.App) *core.Record {
	t.Helper()

	annotations := lesAnnotations(t, app)
	if len(annotations) != 1 {
		t.Fatalf("%d annotation(s) en base, attendu 1", len(annotations))
	}
	return annotations[0]
}

// --- Ce que l'annotation enregistre -------------------------------------------

// L'empreinte de la passe est recopiée sur l'annotation : elle dit quel parser
// elle jugeait, et elle ne se relit pas à l'affichage.
func TestUneAnnotationPorteLEmpreinteDeLaPasseJugee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLeGroupe(t, mux, cookie, passe, "oignon", url.Values{
		champDuVerdict: {"capture trop"},
	})

	annotation := laSeuleAnnotation(t, app)
	if version := annotation.GetString("engine_version"); version != "v0.8.0" {
		t.Errorf("moteur jugé %q, attendu %q", version, "v0.8.0")
	}
	if entrees := annotation.GetInt("lexicon_entries"); entrees != 1234 {
		t.Errorf("lexique jugé de %d entrées, attendu %d", entrees, 1234)
	}
}

// Et c'est celle de l'analyse jugée, non celle de l'analyse courante : une
// passe plus récente ne doit pas récrire le passé.
func TestLEmpreinteEstCelleDeLAnalyseJugeeEtNonDeLaCourante(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	ancienne := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))
	recule(t, app, ancienne, "2026-09-01 10:00:00.000Z")
	passeSignee(t, app, "v0.9.0", 4321,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLeGroupe(t, mux, cookie, ancienne, "oignon", url.Values{
		champDuVerdict: {"capture trop"},
	})

	annotation := laSeuleAnnotation(t, app)
	if version := annotation.GetString("engine_version"); version != "v0.8.0" {
		t.Errorf("moteur jugé %q, attendu %q — l'empreinte a été relue sur la passe courante",
			version, "v0.8.0")
	}
}

// Les deux champs sont distincts en base : l'un cite la ligne brute, l'autre
// parle de la règle. C'est la moitié en base du critère ; l'autre est à l'écran.
func TestLesDeuxChampsDUneAnnotationSontDistinctsEnBase(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLaForme(t, mux, cookie, passe, "2 oignons", url.Values{
		champDeLaNoteLocale:      {"il fallait lire 2 oignons jaunes"},
		champDeLaNotePartageable: {"le motif se trompe sur ce gabarit"},
	})

	annotation := laSeuleAnnotation(t, app)
	if locale := annotation.GetString("local_note"); locale != "il fallait lire 2 oignons jaunes" {
		t.Errorf("note locale %q", locale)
	}
	if partageable := annotation.GetString("shareable_note"); partageable != "le motif se trompe sur ce gabarit" {
		t.Errorf("note partageable %q", partageable)
	}
	if strings.Contains(annotation.GetString("shareable_note"), "il fallait lire") {
		t.Error("la note locale a débordé dans le champ partageable")
	}
}

// La cible est une clé naturelle : l'aliment canonique pour un groupe, la ligne
// brute pour une forme. Les lignes de analyses_formes sont recréées à chaque
// passe, une annotation qui les référencerait ne survivrait pas à la seconde.
func TestLaCibleDUneAnnotationEstUneCleNaturelle(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLeGroupe(t, mux, cookie, passe, "oignon", url.Values{champDuVerdict: {"capture trop"}})
	annoteLaForme(t, mux, cookie, passe, "2 oignons", url.Values{champDeLaNoteLocale: {"mal découpée"}})

	annotations := lesAnnotations(t, app)
	if len(annotations) != 2 {
		t.Fatalf("%d annotation(s), attendu 2", len(annotations))
	}
	if aliment := annotations[0].GetString("food"); aliment != "oignon" {
		t.Errorf("l'annotation de groupe vise %q, attendu %q", aliment, "oignon")
	}
	if brut := annotations[0].GetString("raw"); brut != "" {
		t.Errorf("l'annotation de groupe porte aussi la ligne %q : food seul désigne un groupe", brut)
	}
	if brut := annotations[1].GetString("raw"); brut != "2 oignons" {
		t.Errorf("l'annotation de forme vise %q, attendu %q", brut, "2 oignons")
	}
	if aliment := annotations[1].GetString("food"); aliment != "" {
		t.Errorf("l'annotation de forme porte aussi l'aliment %q : elle trancherait le groupe", aliment)
	}
}

// La saisie crée les mots qui manquent, et l'annotation les porte.
func TestUneAnnotationPorteLesVerdictsSaisis(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLeGroupe(t, mux, cookie, passe, "oignon", url.Values{
		champDuVerdict: {"Capture trop, manque au lexique"},
	})

	annotation := laSeuleAnnotation(t, app)
	if portes := annotation.GetStringSlice("verdicts"); len(portes) != 2 {
		t.Fatalf("%d verdict(s) sur l'annotation, attendu 2", len(portes))
	}
}

// Une annotation qui ne dit rien n'est pas une annotation : ni mot, ni note, ni
// lecture attendue. L'établi lit le carnet, il ne se remplit pas de lignes vides.
func TestUneAnnotationVideEstRefusee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	rec := soumetUneAnnotation(mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {"oignon"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if n := len(lesAnnotations(t, app)); n != 0 {
		t.Errorf("%d annotation(s) écrite(s) alors que le formulaire ne disait rien", n)
	}
}

// Une cible que la passe ne porte pas n'est pas une cible : rien ne s'écrit.
func TestUneAnnotationSurUneCibleAbsenteEstRefusee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	rec := soumetUneAnnotation(mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {"aliment que cette passe n'a jamais lu"},
		champDuVerdict:      {"capture trop"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if n := len(lesAnnotations(t, app)); n != 0 {
		t.Errorf("%d annotation(s) écrite(s) sur une cible que la passe ne porte pas", n)
	}
}

// --- La survie à une seconde passe --------------------------------------------

// Le critère de survie, dans les deux niveaux : la seconde analyse du même
// corpus recrée des lignes neuves, et les deux annotations sont retrouvées —
// l'une sur le même aliment canonique, l'autre sur la même ligne brute.
func TestUneAnnotationEstRetrouveeApresUneSecondeAnalyse(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	premiere := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))
	recule(t, app, premiere, "2026-09-01 10:00:00.000Z")

	annoteLeGroupe(t, mux, cookie, premiere, "oignon",
		url.Values{champDuVerdict: {"capture trop"}})
	annoteLaForme(t, mux, cookie, premiere, "2 oignons",
		url.Values{champDeLaNoteLocale: {"il fallait lire 2 oignons jaunes"}})

	// La seconde passe lit le même corpus : ses formes sont neuves, leurs
	// identifiants aussi.
	seconde := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	groupe, err := annotationsDuGroupe(app, "oignon")
	if err != nil {
		t.Fatalf("annotations du groupe : %v", err)
	}
	if len(groupe) != 1 {
		t.Errorf("%d annotation(s) retrouvée(s) sur le groupe après la seconde passe, attendu 1", len(groupe))
	}

	// Et la fiche de la forme neuve la montre : c'est là que le curateur la
	// retrouve.
	corps := laFiche(t, mux, cookie, laForme(t, app, seconde, "2 oignons"))
	if !strings.Contains(corps, "il fallait lire 2 oignons jaunes") {
		t.Errorf("la fiche de la forme relue ne montre pas l'annotation posée à la passe précédente :\n%s", corps)
	}
}

// --- Ce que les écrans montrent ------------------------------------------------

// Les deux champs sont distincts à l'écran, et pas seulement en base : deux
// colonnes séparées ne servent à rien si la page les mêle.
func TestLesDeuxChampsSontDistinctsALEcran(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLaForme(t, mux, cookie, passe, "2 oignons", url.Values{
		champDeLaNoteLocale:      {"il fallait lire 2 oignons jaunes"},
		champDeLaNotePartageable: {"le motif se trompe sur ce gabarit"},
	})

	corps := laFiche(t, mux, cookie, laForme(t, app, passe, "2 oignons"))
	locale := texteDeLaClasse(corps, "note-locale")
	partageable := texteDeLaClasse(corps, "note-partageable")

	if !strings.Contains(locale, "il fallait lire 2 oignons jaunes") {
		t.Errorf("la note locale ne s'affiche pas dans son propre bloc : %q", locale)
	}
	if !strings.Contains(partageable, "le motif se trompe sur ce gabarit") {
		t.Errorf("la note partageable ne s'affiche pas dans son propre bloc : %q", partageable)
	}
	if strings.Contains(partageable, "il fallait lire") {
		t.Errorf("la note locale s'affiche dans le bloc partageable : %q", partageable)
	}
}

// DOD.md §3 : le texte d'une annotation vient d'un humain et ressort dans une
// page. Il ressort littéralement, balisage compris.
func TestUneAnnotationContenantDuBalisageRessortLitteralement(t *testing.T) {
	const balise = `<script>alert(1)</script>`

	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLaForme(t, mux, cookie, passe, "2 oignons", url.Values{
		champDeLaNoteLocale:      {"local " + balise},
		champDeLaNotePartageable: {"partageable " + balise},
		champDuVerdict:           {balise + " capture trop"},
	})

	corps := laFiche(t, mux, cookie, laForme(t, app, passe, "2 oignons"))
	if strings.Contains(corps, balise) {
		t.Errorf("la fiche rend la balise telle quelle :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("la fiche n'affiche pas le texte de l'annotation :\n%s", corps)
	}
}

// Le fragment d'annotation est écrit une fois et sert aux deux niveaux : la
// cible change, pas le formulaire.
func TestLeFragmentDAnnotationSertLesDeuxNiveaux(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	surLeGroupe := leFragmentDAnnotation(t, mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {"oignon"},
	})
	surLaFiche := laFiche(t, mux, cookie, laForme(t, app, passe, "2 oignons"))

	for nom, corps := range map[string]string{"le groupe": surLeGroupe, "la forme": surLaFiche} {
		for _, attendu := range []string{
			`name="` + champDuVerdict + `"`,
			`name="` + champDeLaNoteLocale + `"`,
			`name="` + champDeLaNotePartageable + `"`,
			`name="` + nomDuChampAttendu + `"`,
		} {
			if !strings.Contains(corps, attendu) {
				t.Errorf("le formulaire sur %s ne porte pas %s :\n%s", nom, attendu, corps)
			}
		}
	}

	// Au niveau de la forme, le mot ne suffit pas : la lecture attendue se
	// saisit champ à champ, et c'est elle la matière d'un jeu annoté.
	if !strings.Contains(surLaFiche, `name="`+champDeLAlimentAttendu+`"`) {
		t.Errorf("la fiche n'offre pas la lecture attendue champ à champ :\n%s", surLaFiche)
	}
	if strings.Contains(surLeGroupe, `name="`+champDeLAlimentAttendu+`"`) {
		t.Errorf("la lecture attendue est offerte sur un groupe, qui n'est pas une ligne :\n%s", surLeGroupe)
	}
}

// La lecture attendue reste du côté local : elle décrit une ligne du corpus.
func TestLaLectureAttendueEstEnregistreeEtAffichee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	annoteLaForme(t, mux, cookie, passe, "2 oignons", url.Values{
		champDeLaQuantiteAttendue: {"2"},
		champDeLAlimentAttendu:    {"oignon jaune"},
	})

	annotation := laSeuleAnnotation(t, app)
	var attendue lectureDUneForme
	if err := annotation.UnmarshalJSONField("expected_reading", &attendue); err != nil {
		t.Fatalf("lecture attendue : %v", err)
	}
	if attendue.Aliment != "oignon jaune" {
		t.Errorf("aliment attendu %q, attendu %q", attendue.Aliment, "oignon jaune")
	}
	if attendue.Quantite == nil || *attendue.Quantite != 2 {
		t.Errorf("quantité attendue %v, attendu 2", attendue.Quantite)
	}

	corps := laFiche(t, mux, cookie, laForme(t, app, passe, "2 oignons"))
	if !strings.Contains(texteDeLaClasse(corps, "lecture-attendue"), "oignon jaune") {
		t.Errorf("la fiche ne montre pas la lecture attendue :\n%s", corps)
	}
}

// --- Les deux vocabulaires ------------------------------------------------------

// Un mot de verdict ne ressort pas dans les suggestions du formulaire de
// recette, et réciproquement : les deux listes ne se mélangent pas.
func TestLesSuggestionsDesDeuxVocabulairesNeSeCroisentPas(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	if _, err := verdictsDepuisSaisie(app, "capitonné"); err != nil {
		t.Fatalf("mot de verdict : %v", err)
	}
	if _, err := tagsDepuisSaisie(app, "capiteux"); err != nil {
		t.Fatalf("tag du carnet : %v", err)
	}

	duCarnet := lesSuggestionsRendues(t, mux, cookie,
		"/tags/suggestions?"+url.Values{"tags": {"capi"}}.Encode())
	if !strings.Contains(duCarnet, "capiteux") {
		t.Errorf("les suggestions du carnet ne proposent pas son propre tag :\n%s", duCarnet)
	}
	if strings.Contains(duCarnet, "capitonné") {
		t.Errorf("un mot de verdict remonte dans les suggestions du carnet :\n%s", duCarnet)
	}

	deLEtabli := lesSuggestionsRendues(t, mux, cookie,
		cheminDesSuggestionsDeVerdicts+"?"+url.Values{champDuVerdict: {"capi"}}.Encode())
	if !strings.Contains(deLEtabli, "capitonné") {
		t.Errorf("les suggestions de l'établi ne proposent pas son propre mot :\n%s", deLEtabli)
	}
	if strings.Contains(deLEtabli, "capiteux") {
		t.Errorf("un tag du carnet remonte dans les suggestions de l'établi :\n%s", deLEtabli)
	}
}

// --- Le droit d'entrer, et le jeton ---------------------------------------------

// Les trois routes de l'annotation sont réservées aux comptes portant le droit
// d'accès à l'établi, et le refus se teste dans le sens du refus.
func TestLesRoutesDeLAnnotationSontReserveesAuxCurateurs(t *testing.T) {
	app, mux := serveurDeLEtabli(t)
	compteParDefaut(t, app)
	ordinaire := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

	for nom, chemin := range map[string]string{
		"le fragment":  cheminDeLAnnotation,
		"les verdicts": cheminDesSuggestionsDeVerdicts,
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

// Le dépôt d'une annotation est refusé à un compte authentifié sans le droit,
// et rien ne s'écrit — la garde passe avant la lecture du corps.
func TestLeDepotDUneAnnotationEstRefuseAUnCompteOrdinaire(t *testing.T) {
	app, mux := serveurDeLEtabli(t)
	compteParDefaut(t, app)
	ordinaire := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	rec := soumetUneAnnotation(mux, ordinaire, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {"oignon"},
		champDuVerdict:      {"capture trop"},
	})

	if rec.Code != http.StatusForbidden {
		t.Fatalf("statut %d, attendu %d — corps :\n%s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if n := len(lesAnnotations(t, app)); n != 0 {
		t.Errorf("%d annotation(s) écrite(s) par un compte sans le droit", n)
	}
}

// Et le jeton anti-rejeu est exigé, comme sur tout POST du dépôt : une page
// tierce qui soumet ce formulaire toute seule n'a aucun moyen de le connaître.
func TestLeDepotDUneAnnotationExigeLeJetonAntiRejeu(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	champs := url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {"oignon"},
		champDuVerdict:      {"capture trop"},
	}
	req := httptest.NewRequest(http.MethodPost, cheminDeLAnnotation,
		strings.NewReader(champs.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("statut %d sans jeton, attendu %d — corps :\n%s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if n := len(lesAnnotations(t, app)); n != 0 {
		t.Errorf("%d annotation(s) écrite(s) sans jeton anti-rejeu", n)
	}
}

// --- Lecture des pages ------------------------------------------------------------

// leFragmentDAnnotation demande le formulaire qui s'ouvre sur une ligne de la
// vue agrégée.
func leFragmentDAnnotation(t *testing.T, mux http.Handler, cookie *http.Cookie, requete url.Values) string {
	t.Helper()

	rec := avecCookie(mux, http.MethodGet,
		cheminDeLAnnotation+"?"+requete.Encode(), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur le fragment d'annotation, attendu %d — corps :\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// lesSuggestionsRendues demande un bloc de suggestions et exige qu'il soit rendu.
func lesSuggestionsRendues(t *testing.T, mux http.Handler, cookie *http.Cookie, cible string) string {
	t.Helper()

	rec := avecCookie(mux, http.MethodGet, cible, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur %s, attendu %d — corps :\n%s",
			rec.Code, cible, http.StatusOK, rec.Body.String())
	}
	return rec.Body.String()
}

// texteDeLaClasse rend le contenu de tous les éléments portant cette classe,
// concaténé. Une lecture textuelle et volontairement bête, comme celle des
// autres écrans de l'établi.
func texteDeLaClasse(corps, classe string) string {
	motif := regexp.MustCompile(`<[a-z]+ class="` + regexp.QuoteMeta(classe) + `">(.*?)</[a-z]+>`)

	var morceaux []string
	for _, trouve := range motif.FindAllStringSubmatch(corps, -1) {
		morceaux = append(morceaux, html.UnescapeString(trouve[1]))
	}
	return strings.Join(morceaux, "\n")
}

// --- Ce que la review de la PR #94 a relevé -----------------------------------------

// Le groupe des lignes dont aucun aliment n'a été lu a pour clé la chaîne vide,
// et une annotation de groupe à clé vide ne se relit nulle part : les filtres
// qui retrouvent les annotations d'un groupe écartent food = "", sans quoi une
// annotation de forme remonterait sur ce groupe. Accepter le dépôt, ce serait
// rendre un 200 puis perdre le verdict. Il est refusé, et rien ne s'écrit.
func TestUneAnnotationSurLeGroupeDesAlimentsVidesEstRefusee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeSansAliment("2 cuillères à soupe", 90),
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	rec := soumetUneAnnotation(mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {""},
		champDuVerdict:      {"capture trop"},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `class="avertissement"`) {
		t.Errorf("le formulaire ne revient pas avec son message :\n%s", rec.Body.String())
	}
	if n := len(lesAnnotations(t, app)); n != 0 {
		t.Errorf("%d annotation(s) écrite(s) sur le groupe des aliments vides", n)
	}
}

// Et la vue agrégée n'offre pas de l'y déposer : le bouton « Annoter » manque
// sur cette ligne-là, et sur elle seule.
func TestLaLigneDesAlimentsVidesNOffrePasDAnnoter(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passeTermineeDeTest(t, app,
		formeSansAliment("2 cuillères à soupe", 90),
		formeResolue("2 oignons", "oignon", "Légumes", 400, SignalMotsPerdus))

	corps := laVueAgregee(t, mux, cookie, nil)

	lignes := regexp.MustCompile(`(?s)<tr class="groupe"><td class="aliment-canonique">(.*?)</td>(.*?)</tr>`).
		FindAllStringSubmatch(corps, -1)
	vues := map[string]bool{}
	for _, ligne := range lignes {
		aliment, reste := ligne[1], ligne[2]
		vues[aliment] = true
		offre := strings.Contains(reste, `class="annoter"`)
		if aliment == "" && offre {
			t.Errorf("la ligne des aliments vides offre d'annoter le groupe : %s", reste)
		}
		if aliment == "oignon" && !offre {
			t.Errorf("la ligne de l'oignon n'offre plus d'annoter le groupe : %s", reste)
		}
	}
	if !vues[""] || !vues["oignon"] {
		t.Fatalf("la vue ne montre pas les deux lignes attendues — lues : %v — corps :\n%s", vues, corps)
	}
}

// DOD.md §3 : la clé du groupe vient de la chaîne de requête, et le fragment
// la réécrit dans un champ caché sans vérifier que la passe la porte. Une
// charge qui ferme l'attribut ressort échappée.
func TestLeFragmentDAnnotationEchappeLAlimentReaffiche(t *testing.T) {
	const charge = `"><script>alert('groupe')</script>`

	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	corps := leFragmentDAnnotation(t, mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {charge},
	})

	exigeSansAucun(t, corps, charge, `alert('groupe')</script>`)
	champ := entreBalises(corps, `name="`+parametreDeLAliment+`" value="`, `"`)
	if html.UnescapeString(champ) != charge {
		t.Errorf("le champ caché porte %q, attendu la charge échappée — corps :\n%s", champ, corps)
	}
}

// Même garde sur un dépôt refusé : la ligne brute postée est réécrite telle
// quelle dans le formulaire qui revient, et elle ressort échappée.
func TestUnDepotRefuseEchappeLaFormeReaffichee(t *testing.T) {
	const charge = `"><script>alert('forme')</script>`

	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	rec := soumetUneAnnotation(mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDeLaForme},
		parametreDeLaForme:  {charge},
		champDuVerdict:      {"capture trop"},
	})
	corps := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(corps, html.EscapeString(messageCibleIntrouvable)) {
		t.Fatalf("statut %d, attendu le formulaire refusé — corps :\n%s", rec.Code, corps)
	}

	exigeSansAucun(t, corps, charge, `alert('forme')</script>`)
	champ := entreBalises(corps, `name="`+parametreDeLaForme+`" value="`, `"`)
	if html.UnescapeString(champ) != charge {
		t.Errorf("le champ caché porte %q, attendu la charge échappée — corps :\n%s", champ, corps)
	}
}

// DOD.md §3, famille « Limites » : une annotation porte au plus autant de mots
// que le schéma lui en permet. Au-delà, le formulaire revient avec son
// message, et rien ne s'écrit — ni l'annotation, ni aucun des mots saisis.
//
// La borne est lue sur le schéma et non sur maxVerdicts : c'est ce qui fait
// rougir ce test le jour où la constante serait augmentée sans le champ.
func TestUneAnnotationAuDelaDeLaBorneDesVerdictsEstRefusee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))

	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		t.Fatalf("collection analyses_annotations : %v", err)
	}
	champ, ok := collection.Fields.GetByName("verdicts").(*core.RelationField)
	if !ok {
		t.Fatalf("analyses_annotations.verdicts n'est pas une relation")
	}

	mots := make([]string, 0, champ.MaxSelect+1)
	for i := range champ.MaxSelect + 1 {
		mots = append(mots, "verdict "+strconv.Itoa(i))
	}

	rec := soumetUneAnnotation(mux, cookie, url.Values{
		parametreDeLAnalyse: {passe.Id},
		champDeLaCible:      {cibleDuGroupe},
		parametreDeLAliment: {"oignon"},
		champDuVerdict:      {strings.Join(mots, ", ")},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `class="avertissement"`) {
		t.Errorf("le formulaire ne revient pas avec son message :\n%s", rec.Body.String())
	}
	if n := len(lesAnnotations(t, app)); n != 0 {
		t.Errorf("%d annotation(s) écrite(s) au-delà de la borne", n)
	}
	crees, err := app.FindRecordsByFilter(collectionDesVerdicts, "id != ''", "", 0, 0)
	if err != nil {
		t.Fatalf("relecture du vocabulaire : %v", err)
	}
	if len(crees) != 0 {
		t.Errorf("%d mot(s) créé(s) par un dépôt refusé", len(crees))
	}
}

// Deux lignes de la vue agrégée peuvent porter leur formulaire ouvert en même
// temps. Le bloc ne doit donc viser son propre champ, ses suggestions et ses
// libellés par aucun identifiant de document : un identifiant répété vise le
// premier formulaire de la page, et le mot choisi pour le groupe B atterrirait
// dans celui du groupe A.
func TestDeuxFormulairesOuvertsNePartagentAucunIdentifiant(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)
	passe := passeSignee(t, app, "v0.8.0", 1234,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus),
		formeResolue("1 carotte", "carotte", "Légumes", 3, SignalMotsPerdus))
	if _, err := verdictsDepuisSaisie(app, "capture trop"); err != nil {
		t.Fatalf("mot de verdict : %v", err)
	}

	var corps strings.Builder
	for _, aliment := range []string{"oignon", "carotte"} {
		corps.WriteString(leFragmentDAnnotation(t, mux, cookie, url.Values{
			parametreDeLAnalyse: {passe.Id},
			champDeLaCible:      {cibleDuGroupe},
			parametreDeLAliment: {aliment},
		}))
	}
	// Et le bloc tel que la route des suggestions le rend, suggestion comprise :
	// c'est lui qui porte le bouton de choix.
	corps.WriteString(lesSuggestionsRendues(t, mux, cookie,
		cheminDesSuggestionsDeVerdicts+"?"+url.Values{champDuVerdict: {"capt"}}.Encode()))
	page := corps.String()
	if !strings.Contains(page, "capture trop") {
		t.Fatalf("le bloc des suggestions ne propose rien, le bouton de choix n'est pas éprouvé :\n%s", page)
	}

	vus := map[string]int{}
	for _, id := range regexp.MustCompile(`\sid="([^"]*)"`).FindAllStringSubmatch(page, -1) {
		vus[id[1]]++
	}
	for id, n := range vus {
		if n > 1 {
			t.Errorf("l'identifiant %q apparaît %d fois avec deux formulaires ouverts", id, n)
		}
	}
	for _, global := range regexp.MustCompile(`hx-(?:target|include|select)="(?:#|\[)[^"]*"`).FindAllString(page, -1) {
		t.Errorf("le formulaire vise un élément par sélecteur de document : %s", global)
	}
}
