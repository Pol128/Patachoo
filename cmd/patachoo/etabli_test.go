package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// L'établi : la page de lancement d'une analyse de corpus.
//
// Les tests jouent le serveur entier, ouvrier compris : ce qui est en cause
// ici n'est pas ce que l'ouvrier fait d'un corpus — analyse_test.go le dit
// déjà — mais ce que la page accepte, ce qu'elle refuse, et ce qu'elle laisse
// derrière elle.

// --- Montage ----------------------------------------------------------------

// atelierDeLEtabli monte le serveur, démarre l'ouvrier d'analyse, et ouvre la
// session d'un compte qui porte le droit.
//
// L'ouvrier est branché puis démarré à la main, comme dans analyse_test.go :
// sans lui, une passe déposée resterait en file et aucune de ces pages ne
// dirait jamais autre chose que « en cours ».
func atelierDeLEtabli(t *testing.T) (core.App, http.Handler, *http.Cookie) {
	t.Helper()

	app, mux := serveurDeLEtabli(t)
	compte := compteParDefaut(t, app)
	faisCurateur(t, app, compte, true)
	return app, mux, cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
}

// serveurDeLEtabli monte le serveur et son ouvrier, sans compte ni session :
// les tests du refus opposé au visiteur et au compte ordinaire s'en servent.
func serveurDeLEtabli(t *testing.T) (core.App, http.Handler) {
	t.Helper()

	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)
	ouvrier := brancheLOuvrierDAnalyse(app, a)

	// Avant le démarrage : un test qui échoue en cours de route laisserait
	// sinon l'ouvrier tourner sur une base que le nettoyage referme.
	t.Cleanup(func() { _ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app}) })
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app}); err != nil {
		t.Fatalf("démarrage du serveur : %v", err)
	}

	return monteLeServeur(t, app, a, ouvrier)
}

// soumetLEtabli poste un formulaire urlencodé sur la route de lancement.
func soumetLEtabli(mux http.Handler, cookie *http.Cookie, champs url.Values) *httptest.ResponseRecorder {
	corps := leJetonEstPose(champs).Encode()
	req := httptest.NewRequest(http.MethodPost, cheminDeLEtabli, strings.NewReader(corps))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieDuJetonDeTest())
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// soumetLeCorpusTeleverse poste le même formulaire en multipart, le corpus
// dans un fichier joint : c'est l'autre moitié de « deux entrées, un seul
// travail », et le seul chemin où le corps part dans un fichier temporaire.
func soumetLeCorpusTeleverse(t *testing.T, mux http.Handler, cookie *http.Cookie, contenu string) *httptest.ResponseRecorder {
	t.Helper()

	corps := &bytes.Buffer{}
	ecrivain := multipart.NewWriter(corps)
	ecritLesChamps(t, ecrivain, url.Values{champSourceDeLEtabli: {sourceFournie}})
	partie, err := ecrivain.CreateFormFile(champCorpusTeleverse, "corpus.txt")
	if err != nil {
		t.Fatalf("partie fichier : %v", err)
	}
	if _, err := io.WriteString(partie, contenu); err != nil {
		t.Fatalf("écriture du corpus : %v", err)
	}
	if err := ecrivain.Close(); err != nil {
		t.Fatalf("clôture du corps multipart : %v", err)
	}

	return joueLeMultipart(mux, cookie, ecrivain.FormDataContentType(), corps)
}

// ecritLesChamps recopie les champs de saisie dans un corps multipart, le
// jeton anti-rejeu compris.
func ecritLesChamps(t *testing.T, ecrivain *multipart.Writer, champs url.Values) {
	t.Helper()

	for nom, valeurs := range leJetonEstPose(champs) {
		for _, valeur := range valeurs {
			if err := ecrivain.WriteField(nom, valeur); err != nil {
				t.Fatalf("champ %q : %v", nom, err)
			}
		}
	}
}

// joueLeMultipart joue la requête de lancement sur un corps déjà bâti.
//
// Le corps est un io.Reader et non une tranche : le test du plafond en envoie
// davantage que ce que la machine a de mémoire ne le justifierait, et il le
// fabrique au fil de la lecture.
func joueLeMultipart(mux http.Handler, cookie *http.Cookie, typeDeContenu string, corps io.Reader) *httptest.ResponseRecorder {
	return joueLeMultipartAnnonce(mux, cookie, typeDeContenu, corps, tailleTue)
}

// tailleTue est le Content-Length d'une requête qui n'en annonce aucun, tel
// que net/http le représente.
const tailleTue int64 = -1

// joueLeMultipartAnnonce joue la même requête en annonçant la taille du corps.
//
// C'est un chemin distinct, et non un détail de montage : httptest.NewRequest
// ne renseigne ContentLength que pour les lecteurs qu'il sait mesurer —
// *bytes.Buffer, *bytes.Reader, *strings.Reader — et le laisse à -1 pour tout
// le reste, dont le io.MultiReader qui fabrique un corps hors plafond. Un
// navigateur, lui, annonce toujours la taille de ce qu'il téléverse. Les deux
// chemins doivent tenir, et seul celui-ci passe par le contrôle optimiste des
// bornes de taille.
func joueLeMultipartAnnonce(mux http.Handler, cookie *http.Cookie, typeDeContenu string, corps io.Reader, taille int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, cheminDeLEtabli, corps)
	req.Header.Set("Content-Type", typeDeContenu)
	req.ContentLength = taille
	req.AddCookie(cookieDuJetonDeTest())
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// lancementAccepte poste la saisie et exige la redirection vers l'établi.
func lancementAccepte(t *testing.T, mux http.Handler, cookie *http.Cookie, champs url.Values) {
	t.Helper()

	rec := soumetLEtabli(mux, cookie, champs)
	exigeLaRedirectionVersLEtabli(t, rec)
}

func exigeLaRedirectionVersLEtabli(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d au lancement, attendu %d — corps :\n%s",
			rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if lieu := rec.Header().Get("Location"); lieu != cheminDeLEtabli {
		t.Errorf("Location = %q, attendu %q", lieu, cheminDeLEtabli)
	}
}

// laDernierePasse rend la passe la plus récemment créée, ou fait échouer le
// test : c'est par elle que les tests retrouvent le travail que la route vient
// de déposer, la redirection ne portant aucun identifiant.
func laDernierePasse(t *testing.T, app core.App) *core.Record {
	t.Helper()

	passes, err := app.FindRecordsByFilter("analyses", "status != ''", "-created", 1, 0)
	if err != nil {
		t.Fatalf("lecture des analyses : %v", err)
	}
	if len(passes) == 0 {
		t.Fatal("aucune analyse en base : la route n'a rien déposé")
	}
	return passes[0]
}

// passeMenee attend que la dernière passe porte le statut voulu.
func passeMenee(t *testing.T, app core.App, statut string) *core.Record {
	t.Helper()

	return attendLeStatut(t, app, laDernierePasse(t, app), statut,
		"la passe déposée par la page n'a pas été menée à son terme")
}

// --- Le droit d'entrer -------------------------------------------------------

// Un visiteur n'atteint ni la page ni la route, et il est renvoyé se connecter
// plutôt que refusé : c'est ce que exigeUnCurateur promet, et il faut le dire
// des deux méthodes de l'établi.
func TestUnVisiteurNAtteintNiLaPageNiLaRouteDeLEtabli(t *testing.T) {
	_, mux := serveurDeLEtabli(t)

	for nom, rec := range map[string]*httptest.ResponseRecorder{
		"page":       avecCookie(mux, http.MethodGet, cheminDeLEtabli, nil),
		"route":      soumetLEtabli(mux, nil, url.Values{champSourceDeLEtabli: {sourceInstance}}),
		"avancement": avecCookie(mux, http.MethodGet, cheminDeLAvancementDeLEtabli, nil),
	} {
		t.Run(nom, func(t *testing.T) {
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("statut %d, attendu %d — corps :\n%s",
					rec.Code, http.StatusSeeOther, rec.Body.String())
			}
			if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
				t.Errorf("Location = %q, attendu %q", lieu, "/connexion")
			}
		})
	}
}

// Un compte ordinaire, authentifié, reçoit un refus — distinctement du
// visiteur ci-dessus, dont la session a peut-être seulement expiré.
func TestUnCompteOrdinaireEstRefuseSurLaPageEtSurLaRoute(t *testing.T) {
	app, mux := serveurDeLEtabli(t)
	compte := compteParDefaut(t, app)
	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

	if relu := relitLeCompte(t, app, compte.Id); relu.GetBool(champCurateur) {
		t.Fatal("le compte par défaut porte le droit : le test ne dirait rien du refus")
	}

	// L'avancement est de la partie : il dit qu'une analyse tourne, ce qu'elle
	// a lu et combien de formes elle en tire. C'est une route de l'établi comme
	// les deux autres, et un oubli de garde y serait invisible.
	for nom, rec := range map[string]*httptest.ResponseRecorder{
		"page":       avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie),
		"route":      soumetLEtabli(mux, cookie, url.Values{champSourceDeLEtabli: {sourceInstance}}),
		"avancement": avecCookie(mux, http.MethodGet, cheminDeLAvancementDeLEtabli, cookie),
	} {
		t.Run(nom, func(t *testing.T) {
			if rec.Code != http.StatusForbidden {
				t.Fatalf("statut %d, attendu %d — corps :\n%s",
					rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}

	if compte, err := app.CountRecords("analyses"); err != nil || compte != 0 {
		t.Errorf("%d analyse(s) en base après le refus, attendu 0 (erreur : %v)", compte, err)
	}
}

// Le POST porte le contrôle anti-rejeu comme les douze autres, et son refus
// est une page, pas du JSON.
//
// Le relevé de antirejeu_test.go le dit déjà pour toutes les routes à la fois ;
// ce test-ci le redit ici pour que le fichier de l'établi porte lui-même la
// preuve du critère, sans avoir à aller la chercher ailleurs.
func TestLeLancementSansJetonAntiRejeuEstRefuseEnHTML(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	// Sans le champ caché, mais avec le cookie : c'est exactement ce qu'une
	// page tierce peut produire.
	req := httptest.NewRequest(http.MethodPost, cheminDeLEtabli,
		strings.NewReader(url.Values{champSourceDeLEtabli: {sourceInstance}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieDuJetonDeTest())
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("statut %d, attendu %d — corps :\n%s",
			rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if typeDeContenu := rec.Header().Get("Content-Type"); !strings.Contains(typeDeContenu, "text/html") {
		t.Errorf("Content-Type %q, attendu du text/html : un formulaire ne lit pas le JSON", typeDeContenu)
	}
	exigeContient(t, rec.Body.String(), "Formulaire expiré")
}

// La paire anti-rejeu tient sur cette page comme sur les autres : le champ
// caché reproduit le cookie de la réponse, faute de quoi le formulaire
// refuserait sa propre soumission.
func TestLaPageDeLEtabliRendLeJetonDeSonCookie(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	rec := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	pose := cookieAntiRejeuDe(rec)
	if pose == nil {
		t.Fatal("aucun cookie anti-rejeu posé par la page de l'établi")
	}
	if jeton := jetonDuFormulaire(t, rec.Body.String()); jeton != pose.Value {
		t.Errorf("le formulaire porte %q, le cookie %q : la paire ne tient pas", jeton, pose.Value)
	}
}

// --- Les deux sources, et leur exclusivité -----------------------------------

// Le même contenu, collé puis téléversé, donne le même travail : mêmes formes,
// mêmes occurrences. C'est la seule garantie qui permette de dire que les deux
// entrées n'en sont qu'une.
func TestLeCorpusColleEtLeCorpusTeleverseDonnentLeMemeTravail(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	const contenu = "100 g de farine\n1 pincée de sel\n100 g de farine\n"

	lancementAccepte(t, mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {contenu},
	})
	colle := occurrencesDesFormes(t, app, passeMenee(t, app, statutTermine))

	exigeLaRedirectionVersLEtabli(t, soumetLeCorpusTeleverse(t, mux, cookie, contenu))
	televerse := occurrencesDesFormes(t, app, passeMenee(t, app, statutTermine))

	if len(colle) == 0 {
		t.Fatal("le corpus collé n'a produit aucune forme")
	}
	if fmt.Sprint(colle) != fmt.Sprint(televerse) {
		t.Errorf("collé %v, téléversé %v : les deux entrées ne donnent pas le même travail",
			colle, televerse)
	}
}

// occurrencesDesFormes rend, par ligne brute, le nombre d'occurrences d'une
// passe : c'est ce qui se compare d'une entrée à l'autre.
func occurrencesDesFormes(t *testing.T, app core.App, passe *core.Record) map[string]int {
	t.Helper()

	comptes := map[string]int{}
	for brut, forme := range formesDe(t, app, passe) {
		comptes[brut] = forme.GetInt("occurrences")
	}
	return comptes
}

// Les lignes vides et les lignes d'espaces ne sont ni lues ni comptées comme
// formes : une base exportée en porte, et elles gonfleraient le rapport de la
// passe sans rien dire du parser.
func TestLesLignesVidesNeSontNiLuesNiComptees(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	lancementAccepte(t, mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {"100 g de farine\n\n   \n\t\n1 pincée de sel\n"},
	})

	passe := passeMenee(t, app, statutTermine)
	if lues := passe.GetInt("lines"); lues != 2 {
		t.Errorf("lines = %d, attendu 2 : les lignes vides ont été comptées", lues)
	}
	if formes := passe.GetInt("forms"); formes != 2 {
		t.Errorf("forms = %d, attendu 2 : une ligne vide est devenue une forme", formes)
	}
}

// Les deux sources sont exclusives : un lancement qui en porte deux est refusé
// avec un message, plutôt que d'en préférer une en silence.
func TestLesDeuxSourcesSontExclusives(t *testing.T) {
	cas := []struct {
		nom     string
		champs  url.Values
		message string
	}{
		{nom: "l'instance et un corpus collé", message: messageSourcesMelees, champs: url.Values{
			champSourceDeLEtabli: {sourceInstance},
			champCorpusColle:     {"100 g de farine"},
		}},
		{nom: "aucune source choisie", message: messageSourceAbsente, champs: url.Values{}},
		{nom: "un corpus fourni, mais vide", message: messageCorpusAbsent, champs: url.Values{
			champSourceDeLEtabli: {sourceFournie},
			champCorpusColle:     {"   \n\n"},
		}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			app, mux, cookie := atelierDeLEtabli(t)

			rec := soumetLEtabli(mux, cookie, c.champs)
			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d, attendu %d : le refus doit rendre la page de lancement",
					rec.Code, http.StatusOK)
			}
			// Le message attendu, et non un refus quelconque : sans lui, le
			// cas mixte passerait pour refusé alors que c'est la base vide qui
			// l'aurait arrêté, et la règle d'exclusivité ne serait plus tenue
			// par rien.
			exigeContient(t, rec.Body.String(), html.EscapeString(c.message),
				`<form`, `name="`+champSourceDeLEtabli+`"`)

			if compte, err := app.CountRecords("analyses"); err != nil || compte != 0 {
				t.Errorf("%d analyse(s) en base après le refus, attendu 0 (erreur : %v)", compte, err)
			}
		})
	}
}

// Le corpus collé et téléversé à la fois est le même refus, sur le chemin
// multipart où le fichier existe vraiment.
func TestUnCorpusColleEtTeleverseALaFoisEstRefuse(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	corps := &bytes.Buffer{}
	ecrivain := multipart.NewWriter(corps)
	ecritLesChamps(t, ecrivain, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {"100 g de farine"},
	})
	partie, err := ecrivain.CreateFormFile(champCorpusTeleverse, "corpus.txt")
	if err != nil {
		t.Fatalf("partie fichier : %v", err)
	}
	if _, err := io.WriteString(partie, "1 pincée de sel\n"); err != nil {
		t.Fatalf("écriture du corpus : %v", err)
	}
	if err := ecrivain.Close(); err != nil {
		t.Fatalf("clôture du corps multipart : %v", err)
	}

	rec := joueLeMultipart(mux, cookie, ecrivain.FormDataContentType(), corps)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	exigeContient(t, rec.Body.String(), html.EscapeString(messageSourcesMelees))
	if compte, err := app.CountRecords("analyses"); err != nil || compte != 0 {
		t.Errorf("%d analyse(s) en base après le refus, attendu 0 (erreur : %v)", compte, err)
	}
}

// Une ligne du corpus qui porte du balisage ressort littéralement : la page
// reprend la saisie refusée pour qu'elle n'ait pas à se retaper, et c'est
// exactement là qu'un échappement manquant s'exploiterait.
func TestUneLigneDeCorpusPorteuseDeBalisageRessortLitteralement(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	const injection = `</textarea><script>alert(1)</script>`

	rec := soumetLEtabli(mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceInstance},
		champCorpusColle:     {injection},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	corps := rec.Body.String()
	if strings.Contains(corps, injection) {
		t.Errorf("la saisie ressort telle quelle dans la page :\n%s", corps)
	}
	exigeContient(t, corps, "&lt;script&gt;")
}

// --- La base de l'instance ---------------------------------------------------

// L'établi lit le carnet, il ne le modifie pas : c'est la seule chose qu'un
// exploitant doit pouvoir tenir pour acquise avant de lancer une passe sur sa
// base. Le relevé porte sur les deux collections, champ à champ.
func TestLeLancementSurLInstanceNeModifieNiRecettesNiIngredients(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	creeRecette(t, app, recetteVoulue{
		titre:       "Tarte aux pommes",
		ingredients: []string{"200 g de farine", "3 pommes", "200 g de farine"},
	})
	creeRecette(t, app, recetteVoulue{
		titre:       "Soupe de potiron",
		ingredients: []string{"1 potiron", "20 cl de crème fraîche"},
	})

	avant := releveDesCollections(t, app, "recipes", "ingredients")

	lancementAccepte(t, mux, cookie, url.Values{champSourceDeLEtabli: {sourceInstance}})
	passe := passeMenee(t, app, statutTermine)

	if source := passe.GetString("source"); source != sourceInstance {
		t.Errorf("source = %q, attendu %q", source, sourceInstance)
	}
	if lues := passe.GetInt("lines"); lues != 5 {
		t.Errorf("lines = %d, attendu 5 : la passe n'a pas lu les lignes de l'instance", lues)
	}
	if formes := passe.GetInt("forms"); formes != 4 {
		t.Errorf("forms = %d, attendu 4 : « 200 g de farine » est posée deux fois", formes)
	}

	if apres := releveDesCollections(t, app, "recipes", "ingredients"); apres != avant {
		t.Errorf("le carnet a changé pendant l'analyse.\navant :\n%s\naprès :\n%s", avant, apres)
	}
}

// releveDesCollections rend le contenu des collections données, trié, sous une
// forme comparable d'un bout à l'autre d'une analyse.
func releveDesCollections(t *testing.T, app core.App, noms ...string) string {
	t.Helper()

	var releve strings.Builder
	for _, nom := range noms {
		enregistrements, err := app.FindRecordsByFilter(nom, "id != ''", "id", 0, 0)
		if err != nil {
			t.Fatalf("lecture de %s : %v", nom, err)
		}
		for _, enregistrement := range enregistrements {
			brut, err := json.Marshal(enregistrement.FieldsData())
			if err != nil {
				t.Fatalf("sérialisation de %s/%s : %v", nom, enregistrement.Id, err)
			}
			fmt.Fprintf(&releve, "%s %s %s\n", nom, enregistrement.Id, brut)
		}
	}
	return releve.String()
}

// Une instance sans la moindre ligne d'ingrédient ne laisse pas un travail qui
// ne se clôt jamais : le lancement est refusé, et rien n'est écrit.
func TestUnLancementSurUneInstanceVideEstRefuse(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	if compte, err := app.CountRecords("ingredients"); err != nil || compte != 0 {
		t.Fatalf("%d ligne(s) d'ingrédient sur une base neuve, attendu 0 (erreur : %v)", compte, err)
	}

	rec := soumetLEtabli(mux, cookie, url.Values{champSourceDeLEtabli: {sourceInstance}})
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	exigeContient(t, rec.Body.String(), html.EscapeString(messageInstanceVide))

	if compte, err := app.CountRecords("analyses"); err != nil || compte != 0 {
		t.Errorf("%d analyse(s) en base, attendu 0 : un travail a été déposé sur une base vide (erreur : %v)",
			compte, err)
	}
}

// --- Le plafond de taille ----------------------------------------------------

// nombreDeMioAffiche capte le plafond tel que la page l'annonce.
var nombreDeMioAffiche = regexp.MustCompile(`(\d+)\s*Mio`)

// Le plafond affiché est celui que le code applique : un chiffre recopié dans
// le gabarit, et le jour où la constante bouge, la page ment.
func TestLePlafondAfficheEstCeluiQueLeCodeApplique(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	rec := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	trouve := nombreDeMioAffiche.FindStringSubmatch(rec.Body.String())
	if trouve == nil {
		t.Fatalf("la page n'annonce aucun plafond de taille :\n%s", rec.Body.String())
	}
	// Le calcul est refait ici plutôt qu'emprunté au code de rendu : un test
	// qui appellerait la fonction d'affichage ne comparerait qu'elle à
	// elle-même.
	if attendu := fmt.Sprint(plafondDuCorpsDeLEtabli / (1024 * 1024)); trouve[1] != attendu {
		t.Errorf("la page annonce %s Mio, le code en applique %s", trouve[1], attendu)
	}
}

// Un corps plus gros que le plafond rend la page de lancement sous un statut
// de refus, avec le plafond en toutes lettres.
//
// En multipart, et c'est tout le sujet : exigeLeJetonAntiRejeu lit le corps
// entier avant le gestionnaire, donc un plafond contrôlé dans celui-ci
// arriverait après coup. Le refus doit être celui de la taille, et non la page
// de jeton expiré qui inviterait à recommencer.
func TestUnCorpusPlusGrosQueLePlafondEstRefuseAvecLePlafond(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	corps, typeDeContenu, _ := corpsHorsPlafond(t)
	rec := joueLeMultipart(mux, cookie, typeDeContenu, corps)

	exigeLeRefusDeTailleEnPage(t, rec)
}

// Le même refus quand la requête annonce sa taille, c'est-à-dire sur le chemin
// du navigateur.
//
// Il est distinct du précédent, et c'est PocketBase qui le rend distinct : son
// BodyLimit est posé sur le routeur racine, très en amont de la borne de
// l'établi, et son contrôle optimiste rend l'erreur sur le seul Content-Length,
// sans passer la main. Une borne posée après lui n'est jamais atteinte, et le
// curateur qui téléverse un corpus hors plafond reçoit du JSON — ce que le
// critère d'acceptation exclut nommément.
func TestUnCorpusHorsPlafondQuiAnnonceSaTailleEstRefuseEnPage(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	corps, typeDeContenu, taille := corpsHorsPlafond(t)
	rec := joueLeMultipartAnnonce(mux, cookie, typeDeContenu, corps, taille)

	exigeLeRefusDeTailleEnPage(t, rec)
}

// Le refus de taille n'ouvre pas l'établi à qui n'y a pas droit.
//
// La page que ce refus rend est la page réservée : elle porte le formulaire de
// lancement et le bloc de la dernière passe — sa source, son statut, sa date,
// ses compteurs. La rendre avant d'avoir contrôlé le droit donnerait tout cela
// à un client anonyme, qui n'aurait qu'à poster plus que le plafond pour
// l'obtenir. Le critère d'acceptation dit qu'un visiteur n'atteint ni la page
// ni la route, et il ne connaît pas d'exception pour les corps trop gros.
//
// Les deux chemins de la borne sont éprouvés : celui du contrôle optimiste,
// quand la taille est annoncée, et celui de la lecture, quand elle ne l'est
// pas — un envoi en Transfer-Encoding: chunked.
func TestUnCorpsHorsPlafondNOuvrePasLEtabliAQuiNYAPasDroit(t *testing.T) {
	app, mux := serveurDeLEtabli(t)
	compteParDefaut(t, app)
	ordinaire := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))

	// Une passe en base : sans elle, la page réservée ne rendrait pas son bloc
	// d'avancement et le test ne dirait rien de ce qui fuit.
	passeEnCours(t, app)

	for nom, attendu := range map[string]struct {
		cookie *http.Cookie
		statut int
	}{
		"visiteur":         {nil, http.StatusSeeOther},
		"compte ordinaire": {ordinaire, http.StatusForbidden},
	} {
		for annonce, taille := range map[string]bool{"taille annoncée": true, "taille tue": false} {
			t.Run(nom+", "+annonce, func(t *testing.T) {
				corps, typeDeContenu, mesure := corpsHorsPlafond(t)
				if !taille {
					mesure = tailleTue
				}
				rec := joueLeMultipartAnnonce(mux, attendu.cookie, typeDeContenu, corps, mesure)

				if rec.Code != attendu.statut {
					t.Fatalf("statut %d, attendu %d — corps :\n%s",
						rec.Code, attendu.statut, rec.Body.String())
				}
				if attendu.statut == http.StatusSeeOther {
					if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
						t.Errorf("Location = %q, attendu %q", lieu, "/connexion")
					}
				}
				exigeSansAucun(t, rec.Body.String(),
					`id="avancement-de-l-etabli"`,
					`name="`+champSourceDeLEtabli+`"`)
			})
		}
	}
}

// corpsHorsPlafond bâtit un corps de lancement multipart plus gros que le
// plafond, et dit la taille qu'il aurait à annoncer.
//
// Le rembourrage est produit au fil de la lecture, et jamais matérialisé :
// c'est ce qui permet de dépasser un plafond de plusieurs dizaines de Mio sans
// les écrire en mémoire.
func corpsHorsPlafond(t *testing.T) (io.Reader, string, int64) {
	t.Helper()

	entete := &bytes.Buffer{}
	ecrivain := multipart.NewWriter(entete)
	ecritLesChamps(t, ecrivain, url.Values{champSourceDeLEtabli: {sourceFournie}})
	if _, err := ecrivain.CreateFormFile(champCorpusTeleverse, "corpus.txt"); err != nil {
		t.Fatalf("partie fichier : %v", err)
	}

	var rembourrage int64 = plafondDuCorpsDeLEtabli + 1
	taille := int64(entete.Len()) + rembourrage
	return io.MultiReader(entete, remplissage(rembourrage)), ecrivain.FormDataContentType(), taille
}

// exigeLeRefusDeTailleEnPage relit le refus attendu : une page, sous un statut
// de refus, avec le plafond en toutes lettres.
func exigeLeRefusDeTailleEnPage(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("statut %d, attendu %d — corps :\n%s",
			rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if typeDeContenu := rec.Header().Get("Content-Type"); !strings.Contains(typeDeContenu, "text/html") {
		t.Errorf("Content-Type %q, attendu du text/html : ni JSON, ni page vide", typeDeContenu)
	}

	corpsRendu := rec.Body.String()
	exigeContient(t, corpsRendu,
		fmt.Sprintf("%d Mio", plafondDuCorpsDeLEtabli/(1024*1024)),
		`name="`+champSourceDeLEtabli+`"`)
	exigeSansAucun(t, corpsRendu, "Formulaire expiré")
}

// remplissage rend n octets de lignes valides, sans jamais les matérialiser.
func remplissage(n int64) io.Reader {
	const ligne = "100 g de farine\n"
	return io.LimitReader(repete(ligne), n)
}

// repete rend la même chaîne indéfiniment.
func repete(motif string) io.Reader {
	return &lecteurRepete{motif: motif}
}

type lecteurRepete struct {
	motif string
	rang  int
}

func (l *lecteurRepete) Read(p []byte) (int, error) {
	ecrits := 0
	for ecrits < len(p) {
		n := copy(p[ecrits:], l.motif[l.rang:])
		l.rang = (l.rang + n) % len(l.motif)
		ecrits += n
	}
	return ecrits, nil
}

// --- Ce que le corpus fourni ne laisse pas derrière lui ----------------------

// Le fichier fourni n'est jamais conservé : ni collection de lignes, ni pièce
// jointe, ni copie dans le carnet. Seules les formes de la passe en gardent
// trace, et c'est ce que le relevé vérifie — le témoin n'apparaît nulle part
// ailleurs.
func TestApresUneAnalyseTermineeLeCorpusNeSubsisteQueDansLesFormes(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	const temoin = "1 zeste de bergamote temoin-du-corpus"

	exigeLaRedirectionVersLEtabli(t, soumetLeCorpusTeleverse(t, mux, cookie,
		temoin+"\n100 g de farine\n"))
	passeMenee(t, app, statutTermine)

	porteuses := lesCollectionsQuiPortent(t, app, "temoin-du-corpus")
	if len(porteuses) != 1 || porteuses[0] != "analyses_formes" {
		t.Errorf("le témoin du corpus se retrouve dans %v, attendu la seule collection %q",
			porteuses, "analyses_formes")
	}
}

// Et après un échec, il ne subsiste nulle part : une passe qui n'aboutit pas
// n'écrit aucune forme, et le corpus n'existait qu'en mémoire.
//
// L'échec est obtenu par le plafond de lignes de l'ouvrier, seul refus qu'un
// corps sous le plafond de taille puisse déclencher : une ligne de deux
// octets, répétée jusqu'à dépasser le compte.
func TestApresUneAnalyseEchoueeLeCorpusNeSubsistePas(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	const temoin = "1 zeste de bergamote temoin-du-corpus"

	corpus := temoin + "\n" + strings.Repeat("x\n", plafondDeLignesDUneAnalyse)
	exigeLaRedirectionVersLEtabli(t, soumetLeCorpusTeleverse(t, mux, cookie, corpus))
	passeMenee(t, app, statutEchec)

	if porteuses := lesCollectionsQuiPortent(t, app, "temoin-du-corpus"); porteuses != nil {
		t.Errorf("le témoin du corpus se retrouve dans %v après un échec, attendu nulle part",
			porteuses)
	}
}

// lesCollectionsQuiPortent rend le nom des collections dont un enregistrement
// contient la chaîne cherchée, quel qu'en soit le champ.
//
// Toutes les collections, et non les seules qu'on soupçonne : ce que le
// critère demande est qu'aucune ligne ne subsiste *quelque part*, et une
// recherche ciblée ne dirait rien de la collection qu'on aurait ajoutée entre
// deux.
func lesCollectionsQuiPortent(t *testing.T, app core.App, cherchee string) []string {
	t.Helper()

	collections, err := app.FindAllCollections()
	if err != nil {
		t.Fatalf("lecture des collections : %v", err)
	}

	var porteuses []string
	for _, collection := range collections {
		if collection.IsView() {
			continue
		}
		enregistrements, err := app.FindAllRecords(collection.Name)
		if err != nil {
			t.Fatalf("lecture de %s : %v", collection.Name, err)
		}
		for _, enregistrement := range enregistrements {
			brut, err := json.Marshal(enregistrement.FieldsData())
			if err != nil {
				t.Fatalf("sérialisation de %s/%s : %v", collection.Name, enregistrement.Id, err)
			}
			if strings.Contains(string(brut), cherchee) {
				porteuses = append(porteuses, collection.Name)
				break
			}
		}
	}
	return porteuses
}

// --- L'avancement ------------------------------------------------------------

// La page montre le travail en cours et son avancement, et le fragment se
// redemande tant que la passe tourne.
func TestLaPageMontreLAvancementDuTravail(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	lancementAccepte(t, mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {"100 g de farine\n1 pincée de sel\n"},
	})
	passeMenee(t, app, statutTermine)

	rec := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeContient(t, rec.Body.String(), `id="avancement-de-l-etabli"`)
}

// Tant que la passe tourne, le bloc se redemande — sur la page comme rendu
// seul. C'est le livrable « et son avancement » : sans ces deux attributs, la
// page montre un compteur figé à l'instant du chargement.
//
// Les deux valeurs sont lues sur les constantes et non recopiées : un chemin
// ou une cadence changés d'un côté feraient tomber ce test plutôt que passer
// inaperçus.
func TestLAvancementDUnePasseEnCoursLeDitEtSeRedemande(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	// Écrite à la main plutôt que lancée : une passe menée par l'ouvrier se
	// clôt en quelques millisecondes, et l'état « en cours » ne serait pas
	// observable de façon reproductible.
	passeEnCours(t, app)

	rafraichissement := []string{
		`hx-get="` + cheminDeLAvancementDeLEtabli + `"`,
		`hx-trigger="` + cadenceDeLAvancementDeLEtabli + `"`,
	}

	page := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if page.Code != http.StatusOK {
		t.Fatalf("statut %d sur la page, attendu %d", page.Code, http.StatusOK)
	}
	exigeContient(t, page.Body.String(), append(rafraichissement, "en cours")...)

	fragment := demande(mux, cheminDeLAvancementDeLEtabli, cookie, map[string]string{"HX-Request": "true"})
	if fragment.Code != http.StatusOK {
		t.Fatalf("statut %d sur le fragment, attendu %d — corps :\n%s",
			fragment.Code, http.StatusOK, fragment.Body.String())
	}
	exigeContient(t, fragment.Body.String(), append(rafraichissement, "en cours")...)
}

// Le fragment rendu à HTMX est le bloc seul : une page entière renvoyée dans
// un hx-target produirait des pages imbriquées.
func TestLAvancementRenduAHTMXEstLeBlocSeul(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	lancementAccepte(t, mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {"100 g de farine\n"},
	})
	passeMenee(t, app, statutTermine)

	rec := demande(mux, cheminDeLAvancementDeLEtabli, cookie, map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	corps := rec.Body.String()
	exigeContient(t, corps, `id="avancement-de-l-etabli"`)
	exigeSansAucun(t, corps, "<html", "<h1", `name="`+champSourceDeLEtabli+`"`)
}

// Le fragment cesse de se redemander quand la passe est close : un onglet
// oublié n'a pas à interroger le serveur toutes les deux secondes pour
// l'éternité.
func TestLAvancementNeSeRedemandePlusUneFoisLaPasseClose(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	lancementAccepte(t, mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {"100 g de farine\n"},
	})
	passeMenee(t, app, statutTermine)

	rec := demande(mux, cheminDeLAvancementDeLEtabli, cookie, map[string]string{"HX-Request": "true"})
	exigeSansAucun(t, rec.Body.String(), "hx-trigger")
}

// Ce que la page dit d'une passe terminée : son statut, sa source et ses
// compteurs, en français et relus sur l'enregistrement.
//
// Les chiffres attendus sont pris sur la passe et non recopiés : le test dit
// que la page rend ce que la base porte, pas qu'elle affiche « 5 ».
func TestLaPageDitLeStatutLaSourceEtLesCompteursDUnePasseTerminee(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	creeRecette(t, app, recetteVoulue{
		titre:       "Tarte aux pommes",
		ingredients: []string{"200 g de farine", "3 pommes", "200 g de farine"},
	})

	lancementAccepte(t, mux, cookie, url.Values{champSourceDeLEtabli: {sourceInstance}})
	passe := passeMenee(t, app, statutTermine)

	rec := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeContient(t, rec.Body.String(),
		"terminée",
		html.EscapeString("la base de l'instance"),
		fmt.Sprintf("<strong>%d</strong> ligne(s) lue(s)", passe.GetInt("lines")),
		fmt.Sprintf("<strong>%d</strong> forme(s) distincte(s)", passe.GetInt("forms")),
		// La date passe par la mise en forme commune, déjà éprouvée
		// ailleurs ; ce qui est en cause ici est qu'elle soit celle de la
		// passe, et non une date vide.
		"lancée le "+dateEnFrancais(passe.GetDateTime("created")),
	)
}

// Et d'une passe échouée : « échouée », pas « en cours ». Une passe morte
// annoncée en cours laisse le curateur attendre un résultat qui ne viendra
// pas.
func TestLaPageDitQuUnePasseSurUnCorpusFourniAEchoue(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	// Close en échec à la main, comme l'ouvrier la laisse quand il abandonne :
	// obtenir l'échec par le plafond de lignes coûterait un corpus d'un demi
	// -million de lignes pour dire la même chose du rendu.
	passe := passeEnCours(t, app)
	passe.Set("status", statutEchec)
	if err := app.Save(passe); err != nil {
		t.Fatalf("clôture de la passe en échec : %v", err)
	}

	rec := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeContient(t, rec.Body.String(), "échouée", "un corpus fourni")
}

// L'ouvrier n'en mène qu'une à la fois, et la page le dit plutôt que de
// laisser croire qu'un second travail est parti.
func TestUnSecondLancementEstRefuseParLaPage(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	// Une passe déjà en cours, écrite comme l'ouvrier l'écrirait : c'est sur
	// la base que le refus se prononce, et non sur un état en mémoire.
	passeEnCours(t, app)

	rec := soumetLEtabli(mux, cookie, url.Values{
		champSourceDeLEtabli: {sourceFournie},
		champCorpusColle:     {"100 g de farine\n"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	exigeContient(t, rec.Body.String(), html.EscapeString(errAnalyseDejaEnCours.Error()))

	passes, err := app.CountRecords("analyses", dbx.HashExp{"status": statutEnCours})
	if err != nil {
		t.Fatalf("décompte des analyses en cours : %v", err)
	}
	if passes != 1 {
		t.Errorf("%d analyse(s) en cours, attendu 1 : un second travail a été déposé", passes)
	}
}

// Une instance qui n'a encore rien analysé rend le bloc tout de même, vide :
// c'est le garde de avancementDeLEtabli, et sans lui le fragment part en
// erreur pour le seul curateur qui n'a jamais rien lancé — celui qui découvre
// la page. Le rafraîchissement d'un onglet ouvert avant le premier lancement
// passe exactement par là.
func TestLAvancementDUneInstanceSansAucunePasseSeRendVide(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	// L'état est celui du montage, et on le dit plutôt que de le supposer :
	// un jour où atelierDeLEtabli déposerait une passe, ce test ne couvrirait
	// plus rien sans que rien ne le signale.
	if compte, err := app.CountRecords("analyses"); err != nil || compte != 0 {
		t.Fatalf("%d analyse(s) au montage, attendu 0 (erreur : %v)", compte, err)
	}

	fragment := demande(mux, cheminDeLAvancementDeLEtabli, cookie, map[string]string{"HX-Request": "true"})
	if fragment.Code != http.StatusOK {
		t.Fatalf("statut %d sur le fragment, attendu %d — corps :\n%s",
			fragment.Code, http.StatusOK, fragment.Body.String())
	}
	exigeContient(t, fragment.Body.String(),
		`id="avancement-de-l-etabli"`,
		// La phrase est du texte de gabarit, pas une valeur interpolée : elle
		// ressort telle qu'elle est écrite, apostrophe comprise.
		"Aucune analyse n'a encore été lancée sur cette instance.")
	// Rien à redemander : sans passe, le rafraîchissement interrogerait le
	// serveur toutes les deux secondes pour ne jamais rien apprendre.
	exigeSansAucun(t, fragment.Body.String(), "hx-get", "hx-trigger")

	page := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if page.Code != http.StatusOK {
		t.Fatalf("statut %d sur la page, attendu %d", page.Code, http.StatusOK)
	}
}

// --- Ce que le gabarit recopie -----------------------------------------------

// Les trois champs du formulaire portent les noms que la route relit.
//
// Le gabarit les écrit en dur, comme tous les gabarits du dépôt ; ce test est
// ce qui tient la promesse faite en tête de ces constantes, qu'aucun partage
// ne tenait. Une lettre changée d'un côté fait tomber ce test, là où elle
// passait jusqu'ici inaperçue — un champ mal nommé se lit comme un champ vide,
// et le formulaire refuserait tout lancement sans que rien ne dise pourquoi.
func TestLaPageNommeSesChampsCommeLaRouteLesRelit(t *testing.T) {
	_, mux, cookie := atelierDeLEtabli(t)

	rec := avecCookie(mux, http.MethodGet, cheminDeLEtabli, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	exigeContient(t, rec.Body.String(),
		`name="`+champSourceDeLEtabli+`"`,
		`name="`+champCorpusColle+`"`,
		`name="`+champCorpusTeleverse+`"`,
		// Et l'adresse du formulaire, pour la même raison : un action
		// recopié finit par désigner une route qui n'est plus branchée.
		`action="`+cheminDeLEtabli+`"`)
}

// Un fichier joint mais vide est un corpus absent, comme le même corpus collé.
//
// La seule présence de la partie fichier suffisait à faire une source : le
// lancement partait, et le curateur obtenait une passe terminée sur zéro
// ligne au lieu du message qui lui dit quoi faire. Deux chemins pour une même
// saisie vide, deux réponses — c'est la réponse, pas le chemin, qui doit se
// tenir.
func TestUnFichierJointMaisVideEstUnCorpusAbsent(t *testing.T) {
	app, mux, cookie := atelierDeLEtabli(t)

	rec := soumetLeCorpusTeleverse(t, mux, cookie, "   \n\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	exigeContient(t, rec.Body.String(), html.EscapeString(messageCorpusAbsent))

	if compte, err := app.CountRecords("analyses"); err != nil || compte != 0 {
		t.Errorf("%d analyse(s) en base après le refus, attendu 0 (erreur : %v)", compte, err)
	}
}
