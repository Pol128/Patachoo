package main

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/Pol128/Patachoo/jsonld"
	"github.com/Pol128/Patachoo/recuperation"
)

// Le suivi d'une fournée : la progression pendant qu'elle tourne, le rapport
// une fois qu'elle est finie. Une seule route pour les deux — c'est la même
// question posée à deux moments —, et c'est le statut du lot qui tranche.
//
// Aucun test ne sort sur le réseau : atelierDeLot arme le piège, et cette
// tâche lit l'état écrit par l'ouvrier, elle ne le produit pas.

// --- Montage ----------------------------------------------------------------

// ligneVoulue décrit une ligne du lot telle que l'ouvrier l'aurait laissée.
//
// Le statut vide vaut « pas encore traitée » : c'est ce que creeLeLot écrit, et
// une progression a besoin de lignes qui n'ont pas encore leur sort.
type ligneVoulue struct {
	url     string
	statut  string
	cause   string
	code    int
	recette *core.Record
}

// lotEnBase écrit une fournée par la route de création, puis pose le sort de
// chaque ligne.
//
// Par creeLeLot, et non par des enregistrements montés à la main : le rapport
// lit ce que la sous-tâche 2 écrit, tag compris, et un montage parallèle ne
// dirait rien de leur accord.
func lotEnBase(t *testing.T, app core.App, titulaire *core.Record, voulues ...ligneVoulue) *core.Record {
	t.Helper()

	urls := make([]string, 0, len(voulues))
	for _, voulue := range voulues {
		urls = append(urls, voulue.url)
	}

	lot, _, err := creeLeLot(app, titulaire, urls, time.Now())
	if err != nil {
		t.Fatalf("création du lot : %v", err)
	}

	for rang, ligne := range lignesDuLot(t, app, lot) {
		voulue := voulues[rang]
		if voulue.statut == "" {
			continue
		}

		ligne.Set("status", voulue.statut)
		ligne.Set("cause", voulue.cause)
		ligne.Set("code", voulue.code)
		if voulue.recette != nil {
			ligne.Set("recipe", voulue.recette.Id)
		}
		if err := app.Save(ligne); err != nil {
			t.Fatalf("sort de la ligne %q : %v", voulue.url, err)
		}
	}
	return lot
}

// clot passe le lot en terminé, comme l'ouvrier le fait quand plus aucune de
// ses lignes n'attend son tour.
func clot(t *testing.T, app core.App, lot *core.Record) *core.Record {
	t.Helper()

	lot.Set("status", statutTermine)
	if err := app.Save(lot); err != nil {
		t.Fatalf("clôture du lot : %v", err)
	}
	return lot
}

// suivi demande le fragment de suivi, comme HTMX le rafraîchit.
func suivi(t *testing.T, mux http.Handler, cookie *http.Cookie, lot *core.Record) string {
	t.Helper()

	rec := demande(mux, lienDuSuivi(lot.Id), cookie, map[string]string{"HX-Request": "true"})
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d sur le suivi du lot, attendu %d", rec.Code, http.StatusOK)
	}
	return rec.Body.String()
}

// motifDUnEchec lit une ligne de la liste des échecs : l'adresse, puis le
// libellé de sa cause.
//
// La classe est lue par préfixe, et non à l'identique : une ligne reprise porte
// « echec reprise ». Et le motif ne s'arrête plus au </li>, puisque la ligne
// porte désormais son formulaire de bascule derrière sa cause.
var motifDUnEchec = regexp.MustCompile(`<li class="echec[^"]*"><code>(.*?)</code> <span class="cause">(.*?)</span>`)

// echecsRendus rend, pour chaque adresse listée, le libellé affiché à côté.
func echecsRendus(corps string) map[string]string {
	rendus := map[string]string{}
	for _, trouve := range motifDUnEchec.FindAllStringSubmatch(corps, -1) {
		rendus[trouve[1]] = trouve[2]
	}
	return rendus
}

// lesOnzeCauses : les dix que la sous-tâche 3 écrit en base, plus celle qu'elle
// pose quand c'est notre propre enregistrement qui a échoué.
func lesOnzeCauses() []ligneVoulue {
	causes := []struct {
		cause string
		code  int
	}{
		{recuperation.Injoignable, 0},
		{recuperation.RefusHTTP, 403},
		{recuperation.RefuseeParPolitique, 0},
		{recuperation.RobotsInterdit, 0},
		{recuperation.DelaiDepasse, 0},
		{recuperation.TailleMax, 0},
		{jsonld.SansRecette, 0},
		{jsonld.AucunBalisage, 0},
		{jsonld.JSONInvalide, 0},
		{jsonld.TitreAbsent, 0},
		{causeEnregistrement, 0},
	}

	lignes := make([]ligneVoulue, 0, len(causes))
	for _, c := range causes {
		lignes = append(lignes, ligneVoulue{
			url:    "https://exemple.fr/" + c.cause,
			statut: statutEchec,
			cause:  c.cause,
			code:   c.code,
		})
	}
	return lignes
}

// --- La session -------------------------------------------------------------

// Sans session, le suivi ne rend rien d'exploitable : ni compte, ni adresse.
func TestLeSuiviDuLotExigeUneSession(t *testing.T) {
	app, mux, _ := atelierDeLot(t)
	lot := lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/secrete", statut: statutImportee})

	rec := avecCookie(mux, http.MethodGet, lienDuSuivi(lot.Id), nil)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d pour un visiteur, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if destination := rec.Header().Get("Location"); destination != "/connexion" {
		t.Errorf("redirection vers %q, attendu %q", destination, "/connexion")
	}
	if strings.Contains(rec.Body.String(), "exemple.fr/secrete") {
		t.Errorf("le suivi a été rendu à un visiteur :\n%s", rec.Body.String())
	}
}

// Un compte connecté ne lit pas la fournée d'un autre. Le test est écrit dans
// le sens du refus (DOD.md §3) : c'est la règle qui protège, pas celle qui
// autorise.
//
// Une 404 et non une 403, comme les routes de note : l'existence du lot d'un
// autre compte n'est pas une information à donner par un code de statut.
func TestLeSuiviRefuseLeLotDunAutreCompte(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	lot := clot(t, app, lotEnBase(t, app, autre,
		ligneVoulue{url: "https://exemple.fr/la-sienne", statut: statutEchec, cause: recuperation.Injoignable}))

	rec := demande(mux, lienDuSuivi(lot.Id), cookie, map[string]string{"HX-Request": "true"})

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d sur le lot d'un autre, attendu %d", rec.Code, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "exemple.fr/la-sienne") {
		t.Errorf("le rapport d'un autre compte a été rendu :\n%s", rec.Body.String())
	}
}

// --- La progression ---------------------------------------------------------

// Pendant que le lot tourne, le fragment donne le compte traité et le total.
// Traité veut dire « qui a son sort », quel qu'il soit : une adresse déjà
// présente et une adresse en échec sont traitées toutes les deux.
func TestLaProgressionRendLeCompteTraiteSurLeTotal(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://a.exemple.fr/une", statut: statutImportee},
		ligneVoulue{url: "https://a.exemple.fr/deux", statut: statutDejaPresente},
		ligneVoulue{url: "https://a.exemple.fr/trois", statut: statutEchec, cause: recuperation.Injoignable},
		ligneVoulue{url: "https://a.exemple.fr/quatre", statut: statutEnCours},
		ligneVoulue{url: "https://a.exemple.fr/cinq"},
	)

	corps := suivi(t, mux, cookie, lot)

	if !strings.Contains(corps, "<strong>3</strong> adresses traitées sur <strong>5</strong>") {
		t.Errorf("la progression ne donne pas 3 sur 5 :\n%s", corps)
	}
}

// Le rafraîchissement s'arrête quand le lot est terminé : le fragment rendu ne
// demande plus rien. Sans ça, chaque rapport ouvert interrogerait le serveur
// toutes les deux secondes, indéfiniment.
func TestLaProgressionSeRafraichitEtLeRapportNon(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/une", statut: statutImportee},
		ligneVoulue{url: "https://exemple.fr/deux"},
	)

	enCours := suivi(t, mux, cookie, lot)
	if !strings.Contains(enCours, `hx-get="`+lienDuSuivi(lot.Id)+`"`) {
		t.Errorf("la progression ne se rafraîchit pas :\n%s", enCours)
	}
	if !strings.Contains(enCours, `hx-trigger="every `) {
		t.Errorf("la progression n'a pas de cadence de rafraîchissement :\n%s", enCours)
	}

	termine := suivi(t, mux, cookie, clot(t, app, lot))
	if strings.Contains(termine, "hx-trigger") || strings.Contains(termine, "hx-get") {
		t.Errorf("le rapport demande encore un rafraîchissement :\n%s", termine)
	}
}

// Un lot qui attend son tour n'est pas un lot qui progresse : l'ouvrier mène
// les fournées l'une après l'autre, et celui qui vient de coller ses adresses a
// le droit de savoir qu'il est derrière quelqu'un d'autre.
func TestUnLotQuiAttendSonTourLeDit(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	titulaire := leCompteDeLaSession(t, app)
	premier := lotEnBase(t, app, titulaire, ligneVoulue{url: "https://a.exemple.fr/une"})
	second := lotEnBase(t, app, titulaire, ligneVoulue{url: "https://b.exemple.fr/une"})

	if corps := suivi(t, mux, cookie, premier); !strings.Contains(corps, "import en cours") {
		t.Errorf("le premier lot ne se dit pas en cours :\n%s", corps)
	}
	if corps := suivi(t, mux, cookie, second); !strings.Contains(corps, "en attente de son tour") {
		t.Errorf("le second lot ne se dit pas en attente :\n%s", corps)
	}

	// Le premier fini, le second progresse à son tour.
	clot(t, app, premier)
	if corps := suivi(t, mux, cookie, second); !strings.Contains(corps, "import en cours") {
		t.Errorf("le second lot n'a pas pris son tour :\n%s", corps)
	}
}

// Échappement (DOD.md §3) : un titre venu d'un site tiers ressort échappé dans
// la progression. Une recette importée est du contenu étranger par nature.
func TestUnTitreHostileEstEchappeDansLaProgression(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	hostile := creeRecette(t, app, recetteVoulue{titre: `<script>alert(1)</script>`})
	lot := lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/une", statut: statutImportee, recette: hostile},
		ligneVoulue{url: "https://exemple.fr/deux"},
	)

	corps := suivi(t, mux, cookie, lot)

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("le titre importé ressort tel quel :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("le titre importé n'apparaît pas échappé :\n%s", corps)
	}
}

// --- Le rapport -------------------------------------------------------------

// Les comptes du rapport sont ceux des lignes en base : M importées, D déjà
// présentes, E en échec, et leur somme fait le total soumis.
func TestLesComptesDuRapportSuiventLesLignesEnBase(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutImportee},
		ligneVoulue{url: "https://exemple.fr/2", statut: statutImportee},
		ligneVoulue{url: "https://exemple.fr/3", statut: statutDejaPresente},
		ligneVoulue{url: "https://exemple.fr/4", statut: statutEchec, cause: recuperation.Injoignable},
		ligneVoulue{url: "https://exemple.fr/5", statut: statutEchec, cause: jsonld.AucunBalisage},
		ligneVoulue{url: "https://exemple.fr/6", statut: statutEchec, cause: recuperation.RefuseeParPolitique},
	))

	corps := suivi(t, mux, cookie, lot)

	for _, attendu := range []string{
		"Fournée de <strong>6</strong> adresses",
		"Importées : <strong>2</strong>",
		"Déjà présentes : <strong>1</strong>",
		"En échec : <strong>3</strong>",
	} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("le rapport ne porte pas %q :\n%s", attendu, corps)
		}
	}
}

// Les familles d'échec se comptent séparément : « injoignable » et « sans
// balisage » n'appellent pas la même réaction, et « refusée par la politique de
// sécurité » n'est pas un défaut du site visé.
func TestLeRapportCompteLesFamillesDEchecSeparement(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutEchec, cause: recuperation.Injoignable},
		ligneVoulue{url: "https://exemple.fr/2", statut: statutEchec, cause: recuperation.RefusHTTP, code: 404},
		ligneVoulue{url: "https://exemple.fr/3", statut: statutEchec, cause: jsonld.AucunBalisage},
		ligneVoulue{url: "https://exemple.fr/4", statut: statutEchec, cause: jsonld.TitreAbsent},
		ligneVoulue{url: "https://exemple.fr/5", statut: statutEchec, cause: jsonld.SansRecette},
		ligneVoulue{url: "https://exemple.fr/6", statut: statutEchec, cause: recuperation.RefuseeParPolitique},
		ligneVoulue{url: "https://exemple.fr/7", statut: statutEchec, cause: causeEnregistrement},
	))

	corps := suivi(t, mux, cookie, lot)

	for _, attendu := range []string{
		"Injoignables : <strong>2</strong>",
		"Sans balisage exploitable : <strong>3</strong>",
		"Refusées par la politique de sécurité : <strong>1</strong>",
		"Enregistrement impossible : <strong>1</strong>",
	} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("le rapport ne porte pas %q :\n%s", attendu, corps)
		}
	}
}

// Chaque cause a son libellé, en français, et deux causes n'affichent jamais le
// même : un rapport qui les confondrait ne dirait pas quoi reprendre.
func TestChaqueCauseADeuxLibelleQuiLuiEstPropre(t *testing.T) {
	attendus := map[string]string{
		recuperation.Injoignable:         "Site injoignable : aucune réponse à cette adresse.",
		recuperation.RefusHTTP:           "Refus du site : code HTTP 403.",
		recuperation.RefuseeParPolitique: "Adresse refusée par la politique de sécurité : elle mène au réseau interne.",
		recuperation.RobotsInterdit:      "Le robots.txt du site interdit de récupérer cette page.",
		recuperation.DelaiDepasse:        "Le site a mis trop de temps à répondre.",
		recuperation.TailleMax:           "Page trop lourde : la lecture s'est arrêtée au plafond.",
		jsonld.SansRecette:               "Balisage trouvé, mais aucune recette dedans.",
		jsonld.AucunBalisage:             "Aucun balisage de recette sur la page.",
		jsonld.JSONInvalide:              "Le balisage de la page est illisible.",
		jsonld.TitreAbsent:               "Recette sans titre : rien à enregistrer sous ce nom.",
		causeEnregistrement:              "Enregistrement refusé par la base de Patachoo.",
	}

	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app), lesOnzeCauses()...))

	rendus := echecsRendus(suivi(t, mux, cookie, lot))
	if len(rendus) != len(attendus) {
		t.Fatalf("%d adresses en échec listées, attendu %d", len(rendus), len(attendus))
	}

	for cause, attendu := range attendus {
		rendu := rendus["https://exemple.fr/"+cause]
		if rendu != html.EscapeString(attendu) {
			t.Errorf("cause %q rendue %q, attendu %q", cause, rendu, html.EscapeString(attendu))
		}
	}

	// L'ensemble des libellés rendus a la taille du nombre de causes : deux
	// causes n'aboutissent jamais au même libellé.
	distincts := map[string]bool{}
	for _, libelle := range rendus {
		distincts[libelle] = true
	}
	if len(distincts) != len(attendus) {
		t.Errorf("%d libellés distincts pour %d causes", len(distincts), len(attendus))
	}
}

// Aucun libellé ne dit « une erreur est survenue » : un rapport dont 28 % des
// lignes sont des échecs doit dire lesquels, pas que quelque chose a raté.
func TestAucunLibelleDeCauseNeDitQuUneErreurEstSurvenue(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app), lesOnzeCauses()...))

	for adresse, libelle := range echecsRendus(suivi(t, mux, cookie, lot)) {
		if strings.Contains(strings.ToLower(libelle), "une erreur est survenue") {
			t.Errorf("le libellé de %s ne dit rien : %q", adresse, libelle)
		}
		if strings.TrimSpace(libelle) == "" {
			t.Errorf("%s est listée sans libellé", adresse)
		}
	}
}

// Une cause que le code ne connaît pas ne rend pas une ligne muette : la
// colonne est un texte libre, et la liste des causes appartient au code qui les
// écrit, pas à la migration.
func TestUneCauseInconnueEstNommeeTelleQuelle(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutEchec, cause: "cause_de_demain"},
	))

	rendu := echecsRendus(suivi(t, mux, cookie, lot))["https://exemple.fr/1"]
	if !strings.Contains(rendu, "cause_de_demain") {
		t.Errorf("la cause inconnue n'est pas nommée : %q", rendu)
	}
}

// Un refus HTTP donne le code obtenu : 403 anti-bot et 404 n'appellent ni la
// même réaction, ni le même libellé.
func TestUnRefusHTTPAfficheLeCodeObtenu(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/interdite", statut: statutEchec, cause: recuperation.RefusHTTP, code: 403},
		ligneVoulue{url: "https://exemple.fr/absente", statut: statutEchec, cause: recuperation.RefusHTTP, code: 404},
	))

	rendus := echecsRendus(suivi(t, mux, cookie, lot))
	if interdite := rendus["https://exemple.fr/interdite"]; !strings.Contains(interdite, "403") {
		t.Errorf("le refus 403 ne donne pas son code : %q", interdite)
	}
	if absente := rendus["https://exemple.fr/absente"]; !strings.Contains(absente, "404") {
		t.Errorf("le refus 404 ne donne pas son code : %q", absente)
	}
	if rendus["https://exemple.fr/interdite"] == rendus["https://exemple.fr/absente"] {
		t.Errorf("403 et 404 rendent le même libellé : %q", rendus["https://exemple.fr/interdite"])
	}
}

// Chaque adresse en échec figure dans la liste, avec sa cause, et reste
// copiable : c'est par elle qu'on reprend une URL à la main dans l'import
// unitaire.
func TestChaqueURLEnEchecFigureAvecSaCause(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/reussie", statut: statutImportee},
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse},
		ligneVoulue{url: "https://exemple.fr/nue", statut: statutEchec, cause: jsonld.AucunBalisage},
	))

	rendus := echecsRendus(suivi(t, mux, cookie, lot))
	if len(rendus) != 2 {
		t.Fatalf("%d adresses listées, attendu 2 : %v", len(rendus), rendus)
	}
	for _, adresse := range []string{"https://exemple.fr/muette", "https://exemple.fr/nue"} {
		if rendus[adresse] == "" {
			t.Errorf("%s n'est pas listée avec sa cause : %v", adresse, rendus)
		}
	}
	// Une adresse qui a abouti n'a rien à faire dans la liste des échecs.
	if _, listee := rendus["https://exemple.fr/reussie"]; listee {
		t.Errorf("une adresse importée est listée en échec : %v", rendus)
	}
}

// Échappement (DOD.md §3) : une adresse soumise est du texte d'utilisateur, et
// une ligne collée peut être tout sauf une URL.
func TestUneURLEnEchecEstEchappeeDansLeRapport(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	// L'adresse passe la validation de la saisie — l'hôte est bon —, et c'est
	// le chemin qui porte la charge.
	hostile := `https://exemple.fr/<script>alert(1)</script>`
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: hostile, statut: statutEchec, cause: recuperation.Injoignable},
	))

	corps := suivi(t, mux, cookie, lot)

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("l'adresse en échec ressort telle quelle :\n%s", corps)
	}
	if !strings.Contains(corps, html.EscapeString(hostile)) {
		t.Errorf("l'adresse en échec n'apparaît pas échappée :\n%s", corps)
	}
}

// Le rapport nomme le tag de la fournée, et y renvoie : c'est par lui que la
// fournée se retrouve après coup, dans la liste filtrée de PATA-16.
func TestLeRapportNommeLeTagDuLotEtYRenvoie(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutImportee},
	))

	tag, err := app.FindRecordById("tags", lot.GetString("tag"))
	if err != nil {
		t.Fatalf("tag du lot : %v", err)
	}

	corps := suivi(t, mux, cookie, lot)
	if !strings.Contains(corps, html.EscapeString(tag.GetString("name"))) {
		t.Errorf("le rapport ne nomme pas le tag %q :\n%s", tag.GetString("name"), corps)
	}
	lien := html.EscapeString(lienVersLeTag(tag.GetString("slug")))
	if !strings.Contains(corps, `href="`+lien+`"`) {
		t.Errorf("le rapport ne renvoie pas au tag par %q :\n%s", lien, corps)
	}
}

// Le rapport d'un lot terminé se relit après coup, et pas seulement au moment
// où il s'achève : demandé hors HTMX, il rend une page entière.
func TestLeRapportDUnLotTermineSeRelitApresCoup(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutImportee},
		ligneVoulue{url: "https://exemple.fr/2", statut: statutEchec, cause: recuperation.RobotsInterdit},
	))

	rec := demande(mux, lienDuSuivi(lot.Id), cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d à la relecture, attendu %d", rec.Code, http.StatusOK)
	}

	corps := rec.Body.String()
	if !strings.Contains(corps, "<!doctype html>") {
		t.Errorf("la relecture ne rend pas une page entière :\n%s", corps)
	}
	if !strings.Contains(corps, "Importées : <strong>1</strong>") {
		t.Errorf("la relecture ne porte pas les comptes :\n%s", corps)
	}
	if !strings.Contains(corps, "exemple.fr/2") {
		t.Errorf("la relecture ne liste pas l'adresse en échec :\n%s", corps)
	}
}

// Le fragment reste un fragment : une page entière renvoyée dans un hx-target
// produit des pages imbriquées (pages.go).
func TestLeSuiviRenduAHTMXNEstQuUnFragment(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1"},
	)

	if corps := suivi(t, mux, cookie, lot); strings.Contains(corps, "<!doctype html>") {
		t.Errorf("le fragment porte la page entière :\n%s", corps)
	}
}

// --- Le raccord avec la page de saisie --------------------------------------

// La confirmation du lancement porte le suivi : sans ce point d'accroche,
// personne n'atteindrait jamais la progression.
func TestLaConfirmationDuLancementPorteLeSuivi(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)

	corps := lotAccepte(t, mux, cookie, strings.Join(urlsDeTest(3), "\n"))

	lot := leSeulLot(t, app)
	if !strings.Contains(corps, `hx-get="`+lienDuSuivi(lot.Id)+`"`) {
		t.Errorf("la confirmation ne branche pas le suivi du lot %s :\n%s", lot.Id, corps)
	}
	if !strings.Contains(corps, fmt.Sprintf("<strong>0</strong> adresses traitées sur <strong>%d</strong>", 3)) {
		t.Errorf("la confirmation ne porte pas la progression :\n%s", corps)
	}
}

// --- Ce qui a disparu sous le rapport ---------------------------------------

// Un tag supprimé depuis l'administration vide la relation : la fournée garde
// son rapport, elle perd seulement le lien qui la retrouve. Une absence n'est
// pas une panne.
func TestLeRapportSeRendEncoreSansSonTag(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutImportee},
	))

	tag, err := app.FindRecordById("tags", lot.GetString("tag"))
	if err != nil {
		t.Fatalf("tag du lot : %v", err)
	}
	if err := app.Delete(tag); err != nil {
		t.Fatalf("suppression du tag : %v", err)
	}

	corps := suivi(t, mux, cookie, lot)
	if !strings.Contains(corps, "Importées : <strong>1</strong>") {
		t.Errorf("le rapport ne se rend plus sans son tag :\n%s", corps)
	}
	if strings.Contains(corps, `href="/recettes?tag=`) {
		t.Errorf("le rapport renvoie à un tag supprimé :\n%s", corps)
	}
}

// Une recette supprimée vide la relation de sa ligne : la progression se tait
// sur la dernière entrée plutôt que de refuser de se rendre.
func TestLaProgressionSeRendEncoreSansLaRecetteEntree(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	entree := creeRecette(t, app, recetteVoulue{titre: "Tarte aux poireaux"})
	lot := lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/1", statut: statutImportee, recette: entree},
		ligneVoulue{url: "https://exemple.fr/2"},
	)

	if err := app.Delete(entree); err != nil {
		t.Fatalf("suppression de la recette : %v", err)
	}

	corps := suivi(t, mux, cookie, lot)
	if !strings.Contains(corps, "<strong>1</strong> adresses traitées sur <strong>2</strong>") {
		t.Errorf("la progression ne se rend plus sans la recette entrée :\n%s", corps)
	}
	if strings.Contains(corps, "Dernière recette entrée") {
		t.Errorf("la progression nomme une recette supprimée :\n%s", corps)
	}
}

// --- La reprise d'une adresse en échec --------------------------------------
//
// Le rapport devient une liste de choses à faire : chaque adresse qui n'a pas
// abouti porte une case que l'utilisateur coche lui-même, et qu'il retrouve
// cochée en revenant sur la page. « Reprise », et jamais « traitée » —
// traitees désigne déjà, plus haut dans ce fichier, les lignes dont l'ouvrier
// connaît le sort.

// bascule poste la case d'une ligne, comme le bouton du rapport le fait.
//
// Par posteNote, qui n'a de la note que son nom : c'est l'envoi urlencodé d'un
// formulaire sans téléversement, et c'en est un.
func bascule(t *testing.T, mux http.Handler, cookie *http.Cookie, lot, ligne string, entetes map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	return posteNote(t, mux, lienDeLaReprise(lot, ligne), cookie, nil, entetes)
}

// enHTMX est ce qu'un navigateur muni de HTMX ajoute à sa requête.
var enHTMX = map[string]string{"HX-Request": "true"}

// laLigne rend la ligne du lot qui porte cette adresse.
func laLigne(t *testing.T, app core.App, lot *core.Record, adresse string) *core.Record {
	t.Helper()

	for _, ligne := range lignesDuLot(t, app, lot) {
		if ligne.GetString("url") == adresse {
			return ligne
		}
	}
	t.Fatalf("aucune ligne %q dans le lot %s", adresse, lot.Id)
	return nil
}

// repriseEnBase relit l'état coché d'une ligne : une assertion sur un
// enregistrement gardé en mémoire ne dirait rien de ce qui est écrit.
func repriseEnBase(t *testing.T, app core.App, ligne *core.Record) bool {
	t.Helper()

	relue, err := app.FindRecordById("import_urls", ligne.Id)
	if err != nil {
		t.Fatalf("relecture de la ligne %s : %v", ligne.Id, err)
	}
	return relue.GetBool("handled")
}

// motifDeLaLigneDEchec capte la classe d'une ligne en échec et son adresse :
// c'est par la classe que le rendu dit qu'elle a été reprise.
var motifDeLaLigneDEchec = regexp.MustCompile(`<li class="(echec[^"]*)"><code>(.*?)</code>`)

// reprisesRendues dit, pour chaque adresse listée, si le rendu la marque
// reprise.
func reprisesRendues(corps string) map[string]bool {
	rendues := map[string]bool{}
	for _, trouve := range motifDeLaLigneDEchec.FindAllStringSubmatch(corps, -1) {
		rendues[trouve[2]] = strings.Contains(trouve[1], "reprise")
	}
	return rendues
}

// Cocher, recharger, décocher, recharger : la bascule marche dans les deux
// sens et l'état survit à la page. C'est tout ce que la tâche demande — sans
// la persistance, la case ne serait qu'un ornement.
func TestUneAdresseEnEchecSeCocheEtSeDecoche(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse},
		ligneVoulue{url: "https://exemple.fr/nue", statut: statutEchec, cause: jsonld.AucunBalisage},
	))
	ligne := laLigne(t, app, lot, "https://exemple.fr/muette")

	if rec := bascule(t, mux, cookie, lot.Id, ligne.Id, enHTMX); rec.Code != http.StatusOK {
		t.Fatalf("statut %d à la bascule, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !repriseEnBase(t, app, ligne) {
		t.Errorf("la ligne n'est pas reprise en base après la bascule")
	}

	rendues := reprisesRendues(suivi(t, mux, cookie, lot))
	if !rendues["https://exemple.fr/muette"] {
		t.Errorf("la ligne cochée n'est pas marquée reprise au rechargement : %v", rendues)
	}
	// La bascule ne coche qu'elle : une case qui en cocherait deux ne dirait
	// plus ce qui reste à faire.
	if rendues["https://exemple.fr/nue"] {
		t.Errorf("la bascule a coché une autre ligne : %v", rendues)
	}

	if rec := bascule(t, mux, cookie, lot.Id, ligne.Id, enHTMX); rec.Code != http.StatusOK {
		t.Fatalf("statut %d au décochage, attendu %d", rec.Code, http.StatusOK)
	}
	if repriseEnBase(t, app, ligne) {
		t.Errorf("la ligne est encore reprise en base après le décochage")
	}
	if reprisesRendues(suivi(t, mux, cookie, lot))["https://exemple.fr/muette"] {
		t.Errorf("la ligne décochée est encore marquée reprise au rechargement")
	}
}

// Le rapport dit d'un coup d'œil ce qui reste à faire : le nombre de lignes
// cochées sur le total des échecs. Sans ce compte, il faut parcourir la liste
// pour savoir où on en est.
func TestLeRapportCompteLesReprisesSurLeTotalDesEchecs(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/reussie", statut: statutImportee},
		ligneVoulue{url: "https://exemple.fr/1", statut: statutEchec, cause: recuperation.Injoignable},
		ligneVoulue{url: "https://exemple.fr/2", statut: statutEchec, cause: recuperation.DelaiDepasse},
		ligneVoulue{url: "https://exemple.fr/3", statut: statutEchec, cause: jsonld.AucunBalisage},
	))

	if corps := suivi(t, mux, cookie, lot); !strings.Contains(corps, "<strong>0</strong> reprises sur <strong>3</strong>") {
		t.Errorf("le rapport ne compte pas 0 reprises sur 3 :\n%s", corps)
	}

	bascule(t, mux, cookie, lot.Id, laLigne(t, app, lot, "https://exemple.fr/2").Id, enHTMX)

	// Le total reste celui des échecs, et non celui de la fournée : l'adresse
	// importée n'a pas de case et n'a rien à faire dans ce compte.
	if corps := suivi(t, mux, cookie, lot); !strings.Contains(corps, "<strong>1</strong> reprises sur <strong>3</strong>") {
		t.Errorf("le rapport ne compte pas 1 reprise sur 3 :\n%s", corps)
	}
}

// La case marche sans JavaScript : le POST d'un formulaire ordinaire rend le
// document entier, comme le reste du produit. HTMX n'évite que le
// rechargement.
func TestLaBasculeSeRendEnPageEntiereSansHTMX(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse},
	))
	ligne := laLigne(t, app, lot, "https://exemple.fr/muette")

	rec := bascule(t, mux, cookie, lot.Id, ligne.Id, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	corps := rec.Body.String()
	if !strings.Contains(corps, "<!doctype html>") {
		t.Errorf("la bascule sans HTMX ne rend pas une page entière :\n%s", corps)
	}
	if !reprisesRendues(corps)["https://exemple.fr/muette"] {
		t.Errorf("la page rendue ne marque pas la ligne reprise :\n%s", corps)
	}
}

// Le formulaire de chaque ligne porte le champ caché : sans lui, le contrôle
// anti-rejeu refuserait la soumission du gabarit que nous rendons nous-mêmes.
func TestChaqueLigneEnEchecPorteSonFormulaireDeReprise(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse},
	))
	ligne := laLigne(t, app, lot, "https://exemple.fr/muette")

	corps := suivi(t, mux, cookie, lot)

	lien := lienDeLaReprise(lot.Id, ligne.Id)
	exigeContient(t, corps,
		`action="`+lien+`"`,
		`hx-post="`+lien+`"`,
		`<input type="hidden" name="_antirejeu"`,
	)
}

// --- Ce que la reprise refuse -----------------------------------------------

// La fournée d'un autre compte ne se coche pas, et le refus est une 404 :
// l'existence du lot d'autrui n'est pas une information à donner par un code de
// statut (DOD.md §3, « Règles d'accès »).
func TestLaRepriseRefuseLeLotDunAutreCompte(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	lot := clot(t, app, lotEnBase(t, app, autre,
		ligneVoulue{url: "https://exemple.fr/la-sienne", statut: statutEchec, cause: recuperation.Injoignable}))
	ligne := laLigne(t, app, lot, "https://exemple.fr/la-sienne")

	rec := bascule(t, mux, cookie, lot.Id, ligne.Id, enHTMX)

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d sur le lot d'un autre, attendu %d", rec.Code, http.StatusNotFound)
	}
	if repriseEnBase(t, app, ligne) {
		t.Errorf("la ligne d'un autre compte a été cochée")
	}
}

// Une ligne qui n'appartient pas au lot cité est refusée de même : l'URL doit
// désigner ce qu'elle prétend désigner, sans quoi le contrôle de propriété
// porterait sur un lot et l'écriture sur un autre.
func TestLaRepriseRefuseUneLigneDunAutreLot(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	titulaire := leCompteDeLaSession(t, app)
	sien := clot(t, app, lotEnBase(t, app, titulaire,
		ligneVoulue{url: "https://exemple.fr/sienne", statut: statutEchec, cause: recuperation.Injoignable}))
	autre := clot(t, app, lotEnBase(t, app, creeCompte(t, app, "autre@exemple.fr", "Autre"),
		ligneVoulue{url: "https://exemple.fr/ailleurs", statut: statutEchec, cause: recuperation.Injoignable}))
	ligne := laLigne(t, app, autre, "https://exemple.fr/ailleurs")

	// Le lot est bien le sien, la ligne ne l'est pas.
	rec := bascule(t, mux, cookie, sien.Id, ligne.Id, enHTMX)

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d sur une ligne hors du lot, attendu %d", rec.Code, http.StatusNotFound)
	}
	if repriseEnBase(t, app, ligne) {
		t.Errorf("une ligne hors du lot cité a été cochée")
	}
}

// Sans jeton anti-rejeu, rien n'est écrit : la bascule est un POST comme les
// autres, et une page tierce ne doit pas pouvoir cocher à la place de
// quelqu'un.
func TestLaRepriseExigeLeJetonAntiRejeu(t *testing.T) {
	app, mux, cookie := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse},
	))
	ligne := laLigne(t, app, lot, "https://exemple.fr/muette")

	rec := joueLePost(t, mux, postDeTest{cible: lienDeLaReprise(lot.Id, ligne.Id)}, cookie, "", jetonDeTest)

	if rec.Code != http.StatusForbidden {
		t.Errorf("statut %d sans jeton, attendu %d", rec.Code, http.StatusForbidden)
	}
	if repriseEnBase(t, app, ligne) {
		t.Errorf("la ligne a été cochée par un POST sans jeton")
	}
}

// Sans session, la bascule n'écrit rien : un visiteur est renvoyé vers la
// connexion, comme sur le suivi.
//
// Le statut est exigé, et non « tout sauf 200 » : sans la session, e.Auth est
// nil et la lecture du lot paniquerait, ce qu'un middleware de PocketBase
// rattrape en 500. Un test qui se contenterait d'un refus prendrait donc cette
// panique pour la règle — c'est ce que la passe de sabotages de la DoD a
// relevé.
func TestLaRepriseExigeUneSession(t *testing.T) {
	app, mux, _ := atelierDeLot(t)
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse},
	))
	ligne := laLigne(t, app, lot, "https://exemple.fr/muette")

	rec := bascule(t, mux, nil, lot.Id, ligne.Id, enHTMX)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d pour un visiteur, attendu %d — corps :\n%s",
			rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if destination := rec.Header().Get("Location"); destination != "/connexion" {
		t.Errorf("redirection vers %q, attendu %q", destination, "/connexion")
	}
	if repriseEnBase(t, app, ligne) {
		t.Errorf("la ligne a été cochée par un visiteur")
	}
}
