package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Les deux sous-commandes de l'établi. Elles sont ce qui permet de dessiner
// les écrans suivants sur des chiffres réels plutôt que sur ceux de la forge :
// sans elles, aucune analyse n'existe avant que PATA-124 soit livrée, et le
// résumé n'aurait rien à résumer.

// --- analyse lancer ---------------------------------------------------------

// Sans argument, la commande analyse la base de l'instance.
func TestLaCommandeLancerAnalyseLaBaseDeLInstance(t *testing.T) {
	app := baseNeuve(t)

	recette := recetteNeuve(t, app)
	for _, brut := range []string{"100 g de farine", "1 pincée de sel"} {
		ligne := ingredientNeuf(t, app, recette, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "lancer")
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	passes, err := app.FindAllRecords("analyses")
	if err != nil {
		t.Fatalf("lecture des analyses : %v", err)
	}
	if len(passes) != 1 {
		t.Fatalf("%d analyse(s) en base, attendu 1", len(passes))
	}
	passe := passes[0]
	if statut := passe.GetString("status"); statut != statutTermine {
		t.Errorf("status = %q, attendu %q", statut, statutTermine)
	}
	if source := passe.GetString("source"); source != sourceInstance {
		t.Errorf("source = %q, attendu %q", source, sourceInstance)
	}
	if lues := passe.GetInt("lines"); lues != 2 {
		t.Errorf("lines = %d, attendu 2", lues)
	}
	// L'identifiant est dans la sortie : sans lui, personne ne peut demander
	// le résumé de ce qui vient d'être lancé.
	if !strings.Contains(sortie, passe.Id) {
		t.Errorf("sortie = %q, attendu qu'elle porte l'identifiant %s", sortie, passe.Id)
	}
	if !strings.Contains(sortie, "2") {
		t.Errorf("sortie = %q, attendu qu'elle donne le nombre de lignes lues", sortie)
	}
}

// Avec un argument, la commande lit un fichier du disque de la machine — et le
// fichier fourni n'est ni copié ni déplacé dans pb_data. C'est la promesse que
// PATA-124 reprendra à son compte pour un téléversement.
func TestLaCommandeLancerLitUnFichierSansLeDeposerDansPbData(t *testing.T) {
	app := baseNeuve(t)

	corpus := filepath.Join(t.TempDir(), "corpus.txt")
	contenu := "100 g de farine\n1 pincée de sel\n\n100 g de farine\n"
	if err := os.WriteFile(corpus, []byte(contenu), 0o600); err != nil {
		t.Fatalf("écriture du corpus : %v", err)
	}

	avant := entreesDe(t, app.DataDir())

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "lancer", corpus)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	passes, err := app.FindAllRecords("analyses")
	if err != nil {
		t.Fatalf("lecture des analyses : %v", err)
	}
	if len(passes) != 1 {
		t.Fatalf("%d analyse(s) en base, attendu 1", len(passes))
	}
	if source := passes[0].GetString("source"); source != sourceFournie {
		t.Errorf("source = %q, attendu %q", source, sourceFournie)
	}
	if lues := passes[0].GetInt("lines"); lues != 3 {
		t.Errorf("lines = %d, attendu 3 — la ligne vide n'est pas comptée", lues)
	}
	if formes := passes[0].GetInt("forms"); formes != 2 {
		t.Errorf("forms = %d, attendu 2", formes)
	}

	// Le fichier est resté où il était, tel qu'il était.
	relu, err := os.ReadFile(corpus)
	if err != nil {
		t.Fatalf("le corpus a été déplacé : %v", err)
	}
	if string(relu) != contenu {
		t.Errorf("le corpus a été réécrit : %q", relu)
	}
	// Et rien à son nom n'est apparu dans le répertoire de données.
	for _, entree := range entreesDe(t, app.DataDir()) {
		if avant[entree] {
			continue
		}
		if strings.Contains(entree, "corpus") {
			t.Errorf("le corpus a été déposé dans pb_data : %s", entree)
		}
	}
}

// entreesDe rend, en ensemble, les chemins du répertoire de données relatifs à
// sa racine. Un ensemble plutôt qu'une liste : les journaux de SQLite
// apparaissent et disparaissent d'une écriture à l'autre, et ce qu'on cherche
// est un nom, pas un compte.
func entreesDe(t *testing.T, racine string) map[string]bool {
	t.Helper()

	entrees := map[string]bool{}
	err := filepath.WalkDir(racine, func(chemin string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relatif, err := filepath.Rel(racine, chemin)
		if err != nil {
			return err
		}
		entrees[relatif] = true
		return nil
	})
	if err != nil {
		t.Fatalf("parcours de %s : %v", racine, err)
	}
	return entrees
}

// Un fichier qui n'existe pas n'est pas une analyse vide : la commande sort en
// erreur, et rien n'est écrit en base.
func TestLaCommandeLancerSurUnFichierAbsentEchoue(t *testing.T) {
	app := baseNeuve(t)

	absent := filepath.Join(t.TempDir(), "rien.txt")
	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "lancer", absent)
	if err == nil {
		t.Fatalf("commande acceptée sur un fichier absent :\n%s", sortie)
	}
	if !aEchoue {
		t.Error("témoin d'échec non levé : le binaire sortirait sur zéro")
	}

	passes, err := app.FindAllRecords("analyses")
	if err != nil {
		t.Fatalf("lecture des analyses : %v", err)
	}
	if len(passes) != 0 {
		t.Errorf("%d analyse(s) en base, attendu aucune", len(passes))
	}
}

// --- analyse resume ---------------------------------------------------------

// Le résumé rend ce qu'il faut pour dessiner les écrans : combien de formes,
// combien par signal, combien de résolus et de non résolus, et la répartition
// par catégorie. Une mesure par ligne, sur la sortie de la commande.
func TestLaCommandeResumeRendLesMesures(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)
	o := nouvelOuvrierDAnalyse(app, horlogeFigee(), a)

	passe, err := o.lance(context.Background(), sourceFournie, corpus(
		"1 pincée de sel",      // résolue, aucun signal, « Herbes et épices »
		"2 tomates",            // résolue, aucun signal, « Légumes »
		"100 g de farine",      // non résolue
		"1 feuille de laurier", // résolue, unité répétée
		"100 g de farine",      // la même forme, une seconde occurrence
	))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "resume", passe.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	attendues := []string{
		"lignes : 5",
		"formes : 4",
		"résolus : 3",
		"non résolus : 1",
		"signal " + SignalNonResolu + " : 1",
		"signal " + SignalUniteRepetee + " : 1",
		"catégorie Légumes : 1",
	}
	for _, mesure := range attendues {
		if !strings.Contains(sortie, mesure) {
			t.Errorf("sortie sans « %s » :\n%s", mesure, sortie)
		}
	}
}

// Un identifiant inconnu sort en erreur, et le binaire avec un code non nul :
// un résumé vide passerait pour une analyse sans forme.
func TestLaCommandeResumeSurUnIdInconnuEchoue(t *testing.T) {
	app := baseNeuve(t)

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "resume", "inconnu00000000")
	if err == nil {
		t.Fatalf("commande acceptée sur un identifiant inconnu :\n%s", sortie)
	}
	if !aEchoue {
		t.Error("témoin d'échec non levé : le binaire sortirait sur zéro")
	}
	if !strings.Contains(err.Error(), "inconnu00000000") {
		t.Errorf("message = %q, attendu qu'il nomme l'identifiant cherché", err)
	}
}
