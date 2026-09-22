package main

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// L'annotation : deux champs, deux niveaux.
//
// Ce qui est en cause dans ce fichier n'est ni la page ni la base, mais les
// deux mécanismes que la tâche pose sous elles : la fonction qui compose ce
// qui pourra partir, et l'empreinte qui dit si un verdict tient encore.

// --- Ce qui pourra partir ----------------------------------------------------

// Le test de non-fuite, et c'est la seule lecture qui rende le critère
// vérifiable : deux colonnes séparées ne prouvent pas qu'on ne lira pas la
// mauvaise. On donne à la fonction une annotation dont le champ local cite la
// ligne brute, et on vérifie que la sortie ne la porte nulle part.
func TestLaChargePartageableNeSortNiLeChampLocalNiLaLigneBrute(t *testing.T) {
	const brut = "20 cl de crème fraîche épaisse de la ferme des Trois-Chênes"

	charge := composeLaChargePartageable(annotationPortee{
		Brut:            brut,
		NoteLocale:      "il fallait lire « crème fraîche », pas « " + brut + " »",
		NotePartageable: "le motif aliment_composé se trompe sur ce gabarit",
		Verdicts: []tagDeVerdict{
			{Slug: "capture-trop", Nom: "capture trop", Valide: true},
		},
		LectureAttendue: lue(20, "cl", "de", "crème fraîche", ""),
	})

	rendu := chargeEnTexte(t, charge)
	for _, interdit := range []string{brut, "il fallait lire", "Trois-Chênes"} {
		if strings.Contains(rendu, interdit) {
			t.Errorf("la charge partageable porte %q :\n%s", interdit, rendu)
		}
	}
	if !strings.Contains(rendu, "aliment_composé") {
		t.Errorf("la charge partageable a perdu la note partageable :\n%s", rendu)
	}
	if !strings.Contains(rendu, "capture-trop") {
		t.Errorf("la charge partageable a perdu le verdict :\n%s", rendu)
	}
}

// Un mot de verdict non validé n'entre pas dans la charge : il est saisi par un
// humain, qui peut y écrire du corpus.
func TestLaChargePartageableEcarteUnVerdictNonValide(t *testing.T) {
	charge := composeLaChargePartageable(annotationPortee{
		NotePartageable: "l'entrée oignon jaune attrape à tort cet alias",
		Verdicts: []tagDeVerdict{
			{Slug: "capture-trop", Nom: "capture trop", Valide: true},
			{Slug: "chez-mamie-du-15-rue-des-lilas", Nom: "chez mamie du 15 rue des lilas"},
		},
	})

	if len(charge.Verdicts) != 1 || charge.Verdicts[0] != "capture-trop" {
		t.Errorf("verdicts partagés %v, attendu [capture-trop] seul", charge.Verdicts)
	}
	if strings.Contains(chargeEnTexte(t, charge), "lilas") {
		t.Error("un verdict non validé est sorti dans la charge partageable")
	}
}

// La fonction est pure : ni base, ni serveur, ni horloge, ni fichier. Elle se
// contente donc d'être appelée deux fois sans rien changer à son entrée — c'est
// le patron des fonctions pures de signaux.go.
func TestLaChargePartageableNeTouchePasALAnnotation(t *testing.T) {
	annotation := annotationPortee{
		Brut:            "2 oignons",
		NoteLocale:      "mal découpé",
		NotePartageable: "le motif se trompe",
		Verdicts:        []tagDeVerdict{{Slug: "capture-trop", Valide: true}},
	}
	avant := annotation

	premiere := composeLaChargePartageable(annotation)
	seconde := composeLaChargePartageable(annotation)

	if annotation.NoteLocale != avant.NoteLocale || annotation.Brut != avant.Brut {
		t.Error("la fonction a modifié l'annotation qu'on lui a donnée")
	}
	if chargeEnTexte(t, premiere) != chargeEnTexte(t, seconde) {
		t.Error("deux appels sur la même annotation ne donnent pas la même charge")
	}
}

// --- L'empreinte de la lecture -----------------------------------------------

// Le verdict vaut tant que la lecture qu'il jugeait n'a pas changé : l'empreinte
// est ce qui répond, et elle ne bouge pas pour une passe qui lit pareil.
func TestLEmpreinteDeLaLectureEstLaMemePourDeuxLecturesIdentiques(t *testing.T) {
	formes := []formeJugee{
		{Brut: "2 oignons", Aliment: "oignon", Resolu: true,
			Lecture: *lue(2, "", "", "oignons", ""), Signaux: []string{SignalMotsPerdus}},
		{Brut: "1 oignon jaune", Aliment: "oignon", Resolu: true,
			Lecture: *lue(1, "", "", "oignon jaune", "")},
	}
	// L'ordre de lecture ne fait pas l'empreinte : c'est le contenu du groupe
	// qui est jugé, pas la page qui l'a ramené.
	inverse := []formeJugee{formes[1], formes[0]}

	if empreinteDeLaLecture(formes) != empreinteDeLaLecture(inverse) {
		t.Error("l'empreinte dépend de l'ordre des formes : un verdict serait périmé par un tri")
	}
	if empreinteDeLaLecture(nil) == empreinteDeLaLecture(formes) {
		t.Error("un groupe vide a la même empreinte qu'un groupe lu")
	}
}

// Et elle bouge dès que la lecture bouge — c'est ce qui fait revenir un groupe
// dans l'ordre par défaut après une montée de version qui l'a relu autrement.
func TestLEmpreinteDeLaLectureChangeAvecChaqueChampJuge(t *testing.T) {
	base := formeJugee{
		Brut: "2 oignons", Aliment: "oignon", Resolu: true,
		Lecture: *lue(2, "", "", "oignons", ""), Signaux: []string{SignalMotsPerdus},
	}
	reference := empreinteDeLaLecture([]formeJugee{base})

	autreLecture := base
	autreLecture.Lecture = *lue(2, "", "", "oignon", "")
	autreAliment := base
	autreAliment.Aliment = "oignon jaune"
	autreResolution := base
	autreResolution.Resolu = false
	autresSignaux := base
	autresSignaux.Signaux = []string{SignalMotsReordonnes}

	for nom, change := range map[string]formeJugee{
		"la lecture champ à champ": autreLecture,
		"l'aliment canonique":      autreAliment,
		"la résolution":            autreResolution,
		"les signaux":              autresSignaux,
	} {
		if empreinteDeLaLecture([]formeJugee{change}) == reference {
			t.Errorf("%s a changé sans que l'empreinte bouge", nom)
		}
	}
}

// Ce que l'empreinte ne juge pas : le corpus a grossi, la ligne est vue plus
// souvent, mais elle est lue pareil. Un verdict rendu sur la lecture tient.
//
// Les quatre champs que l'arbitrage nomme — reading, food, resolved, signals —
// et eux seuls. Les occurrences n'en sont pas : sans ce test, ajouter une
// recette au carnet périmerait tous les verdicts de l'instance.
func TestLEmpreinteDeLaLectureIgnoreLesOccurrences(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	rare := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus))
	courante := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 4000, SignalMotsPerdus))

	avant, err := empreinteDuGroupe(app, rare.Id, "oignon")
	if err != nil {
		t.Fatalf("empreinte de la passe rare : %v", err)
	}
	apres, err := empreinteDuGroupe(app, courante.Id, "oignon")
	if err != nil {
		t.Fatalf("empreinte de la passe courante : %v", err)
	}
	if avant != apres {
		t.Error("l'empreinte compte les occurrences : agrandir le corpus périmerait les verdicts")
	}
}

// L'empreinte se relit depuis la base, et c'est elle qui sera recopiée sur
// l'annotation : le groupe s'y désigne par son aliment canonique.
func TestLEmpreinteDuGroupeSeRelitDepuisLaPasse(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	passe := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus),
		formeResolue("1 oignon jaune", "oignon", "Légumes", 1),
		formeResolue("200 g de farine", "farine", "Épicerie", 2))

	empreinte, err := empreinteDuGroupe(app, passe.Id, "oignon")
	if err != nil {
		t.Fatalf("empreinte du groupe : %v", err)
	}
	if empreinte == "" {
		t.Fatal("empreinte vide sur un groupe qui porte deux formes")
	}

	autre, err := empreinteDuGroupe(app, passe.Id, "farine")
	if err != nil {
		t.Fatalf("empreinte de l'autre groupe : %v", err)
	}
	if autre == empreinte {
		t.Error("deux groupes lus différemment portent la même empreinte")
	}
}

// Celle d'une forme porte sur la seule ligne brute, qui est sa clé naturelle.
func TestLEmpreinteDeLaFormeNeJugeQueSaLigne(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))
	passe := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus),
		formeResolue("1 oignon jaune", "oignon", "Légumes", 1))

	deLaForme, err := empreinteDeLaForme(app, passe.Id, "2 oignons")
	if err != nil {
		t.Fatalf("empreinte de la forme : %v", err)
	}
	duGroupe, err := empreinteDuGroupe(app, passe.Id, "oignon")
	if err != nil {
		t.Fatalf("empreinte du groupe : %v", err)
	}
	if deLaForme == duGroupe {
		t.Error("l'empreinte d'une forme est celle de son groupe entier")
	}

	// Une seconde passe du même corpus lit pareil : l'empreinte est la même,
	// et c'est ce qui fait tenir le verdict.
	seconde := passeTermineeDeTest(t, app,
		formeResolue("2 oignons", "oignon", "Légumes", 3, SignalMotsPerdus),
		formeResolue("1 oignon jaune", "oignon", "Légumes", 1))
	rejouee, err := empreinteDeLaForme(app, seconde.Id, "2 oignons")
	if err != nil {
		t.Fatalf("empreinte de la forme dans la seconde passe : %v", err)
	}
	if rejouee != deLaForme {
		t.Error("la même ligne lue pareil dans deux passes porte deux empreintes")
	}
}

// --- Le vocabulaire des verdicts ---------------------------------------------

// La saisie crée ce qui manque, réutilise ce qui existe, et ne crée jamais un
// doublon de slug : c'est le mécanisme de tags.go, sur l'autre collection.
func TestLaSaisieDUnVerdictCreeCeQuiManqueEtReutiliseLeReste(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))

	premiers, err := verdictsDepuisSaisie(app, "Capture trop, manque au lexique")
	if err != nil {
		t.Fatalf("première saisie : %v", err)
	}
	if len(premiers) != 2 {
		t.Fatalf("%d verdicts créés, attendu 2", len(premiers))
	}

	seconds, err := verdictsDepuisSaisie(app, "capture-trop")
	if err != nil {
		t.Fatalf("seconde saisie : %v", err)
	}
	if len(seconds) != 1 || seconds[0].Id != premiers[0].Id {
		t.Errorf("« capture-trop » a créé un second mot au lieu de retrouver le premier")
	}
}

// Un mot neuf naît non validé : la charge partageable ne l'emportera pas tant
// qu'un humain ne l'aura pas validé.
func TestUnVerdictSaisiNaitNonValide(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))

	crees, err := verdictsDepuisSaisie(app, "chez mamie")
	if err != nil {
		t.Fatalf("saisie : %v", err)
	}
	if len(crees) != 1 {
		t.Fatalf("%d verdicts créés, attendu 1", len(crees))
	}
	if crees[0].GetBool("validated") {
		t.Error("un verdict saisi naît validé : un humain pourrait y écrire du corpus")
	}
}

// Les deux vocabulaires ne se touchent pas : un verdict saisi ne crée aucun tag
// de recette, et réciproquement.
func TestLesDeuxVocabulairesNeSeMelangentPas(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))

	if _, err := verdictsDepuisSaisie(app, "capture trop"); err != nil {
		t.Fatalf("saisie d'un verdict : %v", err)
	}
	if _, err := tagsDepuisSaisie(app, "végétarien"); err != nil {
		t.Fatalf("saisie d'un tag : %v", err)
	}

	exigeUnSeulMot(t, app, "tags", "vegetarien")
	exigeAucunMot(t, app, "tags", "capture-trop")
	exigeUnSeulMot(t, app, "analyses_verdicts", "capture-trop")
	exigeAucunMot(t, app, "analyses_verdicts", "vegetarien")
}

// --- Montage ------------------------------------------------------------------

// chargeEnTexte rend la charge telle qu'elle partirait : du JSON, puisque c'est
// ce qu'un envoi vers patachoo.org transporterait. Le test de non-fuite se lit
// sur cette sortie-là, et non champ par champ — c'est ce qui le tient le jour
// où un champ s'ajoute.
func chargeEnTexte(t *testing.T, charge chargePartageable) string {
	t.Helper()

	rendu, err := jsonDeLaCharge(charge)
	if err != nil {
		t.Fatalf("sérialisation de la charge : %v", err)
	}
	return rendu
}

func exigeUnSeulMot(t *testing.T, app core.App, collection, slug string) {
	t.Helper()

	if _, err := app.FindFirstRecordByData(collection, "slug", slug); err != nil {
		t.Errorf("%s ne porte pas %q : %v", collection, slug, err)
	}
}

func exigeAucunMot(t *testing.T, app core.App, collection, slug string) {
	t.Helper()

	if _, err := app.FindFirstRecordByData(collection, "slug", slug); err == nil {
		t.Errorf("%s porte %q, qui appartient à l'autre vocabulaire", collection, slug)
	}
}
