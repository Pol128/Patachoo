package main

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// L'ouvrier d'analyse : une passe sur un corpus, ses formes, ses plafonds.
//
// Aucun test ne patiente : le temps est une horloge réglée qui n'avance que
// lorsqu'on la lit, d'un pas choisi par le test. C'est ce qui permet de
// vérifier un délai de trente minutes et une cadence d'avancement d'une
// seconde en quelques millisecondes.

// --- L'horloge réglée -------------------------------------------------------

// horlogeReglee avance d'un pas fixe à chaque lecture. Un pas nul la fige.
//
// L'horloge virtuelle de l'ouvrier d'import ne convient pas ici : elle
// n'avance que lorsque toutes les files annoncées attendent, or une analyse
// n'attend jamais — elle lit le temps pour savoir si elle a débordé, et rien
// ne la ferait donc avancer.
type horlogeReglee struct {
	mu         sync.Mutex
	maintenant time.Time
	pas        time.Duration
}

func horlogeFigee() *horlogeReglee {
	return &horlogeReglee{maintenant: departDesTests}
}

func horlogeAuPas(pas time.Duration) *horlogeReglee {
	return &horlogeReglee{maintenant: departDesTests, pas: pas}
}

func (h *horlogeReglee) Maintenant() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.maintenant = h.maintenant.Add(h.pas)
	return h.maintenant
}

// Attends rend la main aussitôt : le temps de ces tests est celui des lectures.
func (h *horlogeReglee) Attends(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

func (h *horlogeReglee) Files(int) {}

// --- Montage ----------------------------------------------------------------

// atelierDAnalyse monte une base neuve et l'ouvrier d'analyse qui la sert.
func atelierDAnalyse(t *testing.T, h horlogeDuLot) (core.App, *analyseur, *ouvrierDAnalyse) {
	t.Helper()

	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)
	return app, a, nouvelOuvrierDAnalyse(app, h, a)
}

// corpus rend une source de lignes bâtie sur une tranche : c'est ce que
// PATA-124 branchera sur un téléversement, et ce que la sous-commande branche
// sur un fichier.
func corpus(lignes ...string) sourceDeLignes {
	return func(yield func(string, error) bool) {
		for _, ligne := range lignes {
			if !yield(ligne, nil) {
				return
			}
		}
	}
}

// corpusRepete rend n fois la même ligne, sans jamais la matérialiser : c'est
// ce qui permet d'éprouver le plafond de lignes sans écrire un demi-million de
// chaînes en mémoire.
func corpusRepete(ligne string, n int) sourceDeLignes {
	return func(yield func(string, error) bool) {
		for range n {
			if !yield(ligne, nil) {
				return
			}
		}
	}
}

// formesDe rend les formes d'une passe, par leur ligne brute.
func formesDe(t *testing.T, app core.App, passe *core.Record) map[string]*core.Record {
	t.Helper()

	formes, err := app.FindAllRecords("analyses_formes", dbx.HashExp{"analysis": passe.Id})
	if err != nil {
		t.Fatalf("lecture des formes de l'analyse %s : %v", passe.Id, err)
	}
	parBrut := make(map[string]*core.Record, len(formes))
	for _, forme := range formes {
		parBrut[forme.GetString("raw")] = forme
	}
	return parBrut
}

// passeRelue relit la passe : l'exemplaire que le test tient en main est
// périmé dès que l'ouvrier a écrit.
func passeRelue(t *testing.T, app core.App, passe *core.Record) *core.Record {
	t.Helper()

	relue, err := app.FindRecordById("analyses", passe.Id)
	if err != nil {
		t.Fatalf("relecture de l'analyse %s : %v", passe.Id, err)
	}
	return relue
}

// --- Le compte des formes ---------------------------------------------------

// Le cas nominal : un corpus où des lignes se répètent rend une forme par
// ligne distincte, et chaque forme porte le nombre de fois qu'elle a été vue.
// C'est la mesure dont les écrans suivants se serviront pour trier.
func TestLAnalyseDedupliqueLesLignesEnFormes(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	passe, err := o.lance(context.Background(), sourceFournie, corpus(
		"100 g de farine",
		"1 pincée de sel",
		"100 g de farine",
		"  1 pincée de sel  ",
		"2 tomates",
	))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	relue := passeRelue(t, app, passe)
	if lues := relue.GetInt("lines"); lues != 5 {
		t.Errorf("lines = %d, attendu 5 — les cinq lignes non vides du corpus", lues)
	}
	if formes := relue.GetInt("forms"); formes != 3 {
		t.Errorf("forms = %d, attendu 3 — deux lignes répétées ne font qu'une forme", formes)
	}
	if statut := relue.GetString("status"); statut != statutTermine {
		t.Errorf("status = %q, attendu %q", statut, statutTermine)
	}
	if relue.GetDateTime("finished").IsZero() {
		t.Error("finished est vide : la fin d'une passe ne se déduit pas de updated")
	}

	formes := formesDe(t, app, passe)
	if len(formes) != 3 {
		t.Fatalf("%d forme(s) écrite(s), attendu 3 : %v", len(formes), formes)
	}
	// La forme est la ligne brute, espaces de bord retirés : « 1 pincée de
	// sel » écrit avec des espaces autour est la même forme.
	for brut, attendues := range map[string]int{
		"100 g de farine": 2,
		"1 pincée de sel": 2,
		"2 tomates":       1,
	} {
		forme, ecrite := formes[brut]
		if !ecrite {
			t.Errorf("aucune forme %q", brut)
			continue
		}
		if obtenues := forme.GetInt("occurrences"); obtenues != attendues {
			t.Errorf("occurrences de %q = %d, attendu %d", brut, obtenues, attendues)
		}
	}
}

// Une ligne vide ou d'espaces seuls n'est ni lue ni comptée : le corpus d'une
// base exportée en porte, et elles ne sont pas des ingrédients.
func TestUneLigneVideNEstNiLueNiComptee(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	passe, err := o.lance(context.Background(), sourceFournie, corpus(
		"", "   ", "\t\t", "1 pincée de sel", "  ",
	))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	relue := passeRelue(t, app, passe)
	if lues := relue.GetInt("lines"); lues != 1 {
		t.Errorf("lines = %d, attendu 1 — seule « 1 pincée de sel » est une ligne", lues)
	}
	if formes := relue.GetInt("forms"); formes != 1 {
		t.Errorf("forms = %d, attendu 1", formes)
	}

	if forme, ecrite := formesDe(t, app, passe)[""]; ecrite {
		t.Errorf("une forme vide a été écrite : %v", forme.FieldsData())
	}
}

// La forme porte ce que la relecture demandera : la lecture du moteur, le
// motif qui l'a produite, l'aliment canonique, sa résolution, sa catégorie et
// ses signaux. Sans ces colonnes, l'écran d'annotation n'a rien à montrer.
func TestLaFormePorteLaLectureEtSesSignaux(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	passe, err := o.lance(context.Background(), sourceFournie, corpus(
		"1 pincée de sel",
		"100 g de farine",
		"1 feuille de laurier",
	))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}
	formes := formesDe(t, app, passe)

	// Résolue par le lexique : l'aliment canonique, la catégorie de son
	// entrée, et aucun signal.
	sel, ecrite := formes["1 pincée de sel"]
	if !ecrite {
		t.Fatalf("aucune forme pour « 1 pincée de sel » : %v", formes)
	}
	if aliment := sel.GetString("food"); aliment != "sel" {
		t.Errorf("food = %q, attendu « sel »", aliment)
	}
	if !sel.GetBool("resolved") {
		t.Error("resolved = faux pour « sel », que le lexique résout")
	}
	if categorie := sel.GetString("category"); categorie != "Herbes et épices" {
		t.Errorf("category = %q, attendu « Herbes et épices »", categorie)
	}
	if signaux := signauxDe(t, sel); len(signaux) != 0 {
		t.Errorf("signals = %q, attendu aucun signal sur une ligne lue et résolue", signaux)
	}
	if motif := sel.GetString("pattern"); motif == "" {
		t.Error("pattern est vide : le motif du moteur est ce qui dit quelle règle a lu la ligne")
	}
	// La lecture champ à champ, telle que la page détail la relira.
	if lecture := sel.GetString("reading"); !strings.Contains(lecture, "pincée") {
		t.Errorf("reading = %q, attendu l'unité lue", lecture)
	}

	// Le trou de lexique : l'aliment sort tel quel, non résolu, sans
	// catégorie, et le signal le dit.
	farine, ecrite := formes["100 g de farine"]
	if !ecrite {
		t.Fatalf("aucune forme pour « 100 g de farine » : %v", formes)
	}
	if farine.GetBool("resolved") {
		t.Error("resolved = vrai pour « farine », que le lexique ne résout pas")
	}
	if categorie := farine.GetString("category"); categorie != "" {
		t.Errorf("category = %q, attendu vide : une forme non résolue n'a pas de catégorie", categorie)
	}
	if signaux := signauxDe(t, farine); len(signaux) != 1 || signaux[0] != SignalNonResolu {
		t.Errorf("signals = %q, attendu [%s]", signaux, SignalNonResolu)
	}

	// L'unité répétée : « 1 feuille de feuille de laurier » sur la fiche.
	laurier, ecrite := formes["1 feuille de laurier"]
	if !ecrite {
		t.Fatalf("aucune forme pour « 1 feuille de laurier » : %v", formes)
	}
	if signaux := signauxDe(t, laurier); len(signaux) != 1 || signaux[0] != SignalUniteRepetee {
		t.Errorf("signals = %q, attendu [%s]", signaux, SignalUniteRepetee)
	}
}

// signauxDe rend les signaux écrits sur une forme. La colonne est du JSON :
// c'est json_array_length qui filtrera « au moins un signal ».
func signauxDe(t *testing.T, forme *core.Record) []string {
	t.Helper()

	var signaux []string
	if err := forme.UnmarshalJSONField("signals", &signaux); err != nil {
		t.Fatalf("lecture des signaux de la forme %s : %v", forme.Id, err)
	}
	return signaux
}

// --- L'empreinte de la passe ------------------------------------------------

// Une annotation portée sur une forme ne veut plus rien dire si l'on ne sait
// pas quel parser elle jugeait : la passe porte son empreinte dès sa création,
// et donc même quand elle échoue.
func TestLaPassePorteLEmpreinteDuMoteur(t *testing.T) {
	app, a, o := atelierDAnalyse(t, horlogeFigee())

	passe, err := o.lance(context.Background(), sourceFournie, corpus("1 pincée de sel"))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	relue := passeRelue(t, app, passe)
	if empreinte := relue.GetString("engine_version"); empreinte == "" {
		t.Error("engine_version est vide : sans elle, une annotation devient illisible à la montée de version")
	}
	if formes := relue.GetInt("lexicon_entries"); formes != a.lexique.Formes() {
		t.Errorf("lexicon_entries = %d, attendu %d — les formes du lexique de la passe",
			formes, a.lexique.Formes())
	}
}

// La version du moteur se lit dans les dépendances de l'estampille du binaire,
// et vaut « inconnue » quand l'estampille manque — comme version.go l'assume
// déjà pour la sienne. La fonction reçoit ses informations plutôt que de les
// lire : go test n'inscrit aucune dépendance dans ReadBuildInfo, et une
// fonction qui l'appellerait elle-même ne serait vérifiable sur aucun des cas.
func TestVersionDuMoteur(t *testing.T) {
	cas := []struct {
		nom     string
		infos   *debug.BuildInfo
		attendu string
	}{
		{
			nom: "la dépendance est estampillée",
			infos: &debug.BuildInfo{Deps: []*debug.Module{
				{Path: "github.com/pocketbase/pocketbase", Version: "v0.39.11"},
				{Path: moduleDuMoteur, Version: "v0.1.2"},
			}},
			attendu: "v0.1.2",
		},
		{
			nom:     "aucune dépendance inscrite",
			infos:   &debug.BuildInfo{},
			attendu: versionDuMoteurInconnue,
		},
		{
			nom:     "aucune estampille",
			infos:   nil,
			attendu: versionDuMoteurInconnue,
		},
	}
	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if obtenue := versionDuMoteur(c.infos); obtenue != c.attendu {
				t.Errorf("versionDuMoteur = %q, attendu %q", obtenue, c.attendu)
			}
		})
	}
}

// --- Un travail à la fois ---------------------------------------------------

// Deux analyses menées de front sur la même instance se disputeraient la
// machine et rendraient l'avancement illisible. Le refus se prononce à la mise
// en file, et il se dit.
func TestUnSecondLancementEstRefuse(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	if _, err := o.metEnFile(sourceFournie, corpus("1 pincée de sel")); err != nil {
		t.Fatalf("première mise en file refusée : %v", err)
	}

	_, err := o.lance(context.Background(), sourceInstance, corpus("2 tomates"))
	if err == nil {
		t.Fatal("second lancement accepté, attendu un refus")
	}
	if !strings.Contains(err.Error(), "déjà en cours") {
		t.Errorf("message = %q, attendu qu'il dise qu'une analyse est déjà en cours", err)
	}

	// Et la file n'a pas gagné de seconde passe au passage.
	passes, err := app.FindAllRecords("analyses")
	if err != nil {
		t.Fatalf("lecture des analyses : %v", err)
	}
	if len(passes) != 1 {
		t.Errorf("%d analyse(s) en base, attendu 1 — le refus ne crée rien", len(passes))
	}
}

// --- Les trois plafonds -----------------------------------------------------

// Le plafond de lignes refuse un fichier qui n'est manifestement pas un corpus
// d'ingrédients. Le message donne la valeur : c'est ce qui permet de la
// changer en relisant une ligne.
func TestLePlafondDeLignesRefuseLeCorpus(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	passe, err := o.lance(context.Background(), sourceFournie,
		corpusRepete("1 pincée de sel", plafondDeLignesDUneAnalyse+1))
	if err == nil {
		t.Fatal("corpus accepté, attendu un refus au-delà du plafond de lignes")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(plafondDeLignesDUneAnalyse)) {
		t.Errorf("message = %q, attendu qu'il donne le plafond %d", err, plafondDeLignesDUneAnalyse)
	}
	if statut := passeRelue(t, app, passe).GetString("status"); statut != statutEchec {
		t.Errorf("status = %q, attendu %q — un corpus refusé ne reste pas en cours", statut, statutEchec)
	}
}

// Le plafond de taille est borné sur les octets lus, avant tout découpage en
// lignes : une seule ligne démesurée n'atteindrait jamais le compteur de
// lignes.
//
// Et la borne porte sur la lecture elle-même, pas sur ce qui en ressort : un
// corpus refusé n'est pas d'abord lu en entier. C'est ce qui borne la mémoire
// que le découpage peut demander — sans quoi une ligne de cent mégaoctets
// serait rassemblée avant d'être refusée.
func TestLePlafondDOctetsRefuseUneLigneDemesuree(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	// Bien plus que le plafond : ce qui compte est qu'il n'en soit pas lu
	// autant.
	compteur := &lecteurCompte{flux: io.LimitReader(octetsInfinis{}, plafondDOctetsDUneAnalyse+8<<20)}
	passe, err := o.lance(context.Background(), sourceFournie, lignesDUnReader(compteur))
	if err == nil {
		t.Fatal("corpus accepté, attendu un refus au-delà du plafond de taille")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(plafondDOctetsDUneAnalyse)) {
		t.Errorf("message = %q, attendu qu'il donne le plafond %d octets",
			err, plafondDOctetsDUneAnalyse)
	}
	if statut := passeRelue(t, app, passe).GetString("status"); statut != statutEchec {
		t.Errorf("status = %q, attendu %q", statut, statutEchec)
	}
	if formes := formesDe(t, app, passe); len(formes) != 0 {
		t.Errorf("%d forme(s) écrite(s), attendu aucune : le corpus n'a jamais été découpé", len(formes))
	}
	if lus := compteur.lus.Load(); lus > plafondDOctetsDUneAnalyse+1 {
		t.Errorf("%d octets lus pour refuser le corpus, attendu au plus %d : la lecture ne s'arrête pas au plafond",
			lus, plafondDOctetsDUneAnalyse+1)
	}
}

// Le même plafond vaut quand les octets arrivent ligne à ligne, d'une source
// qui n'est pas un flux — celle de la base de l'instance : c'est le nombre
// d'octets lus qui est borné, pas la longueur d'une ligne.
func TestLePlafondDOctetsRefuseUnCorpusTropGros(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	// Mille octets par ligne, et de quoi dépasser le plafond bien avant le
	// demi-million de lignes.
	ligne := strings.Repeat("a", 1_000)
	lignes := plafondDOctetsDUneAnalyse/len(ligne) + 1

	passe, err := o.lance(context.Background(), sourceFournie, corpusRepete(ligne, lignes))
	if err == nil {
		t.Fatal("corpus accepté, attendu un refus au-delà du plafond de taille")
	}
	if !strings.Contains(err.Error(), fmt.Sprint(plafondDOctetsDUneAnalyse)) {
		t.Errorf("message = %q, attendu qu'il donne le plafond %d octets",
			err, plafondDOctetsDUneAnalyse)
	}
	if statut := passeRelue(t, app, passe).GetString("status"); statut != statutEchec {
		t.Errorf("status = %q, attendu %q", statut, statutEchec)
	}
}

// octetsInfinis rend des octets sans jamais de fin de ligne : c'est la ligne
// démesurée, sans avoir à la garder en mémoire dans le test.
type octetsInfinis struct{}

func (octetsInfinis) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

// lecteurCompte note combien d'octets ont vraiment été demandés au corpus.
type lecteurCompte struct {
	flux io.Reader
	lus  atomic.Int64
}

func (l *lecteurCompte) Read(p []byte) (int, error) {
	n, err := l.flux.Read(p)
	l.lus.Add(int64(n))
	return n, err
}

// Le plafond de durée arrête une passe qui déborde : le travail passe en
// échec, il ne se poursuit pas.
func TestLeDelaiInterromptLAnalyse(t *testing.T) {
	// Une minute par lecture de l'horloge : la trentième lecture dépasse les
	// trente minutes, bien avant la centième ligne.
	app, _, o := atelierDAnalyse(t, horlogeAuPas(time.Minute))

	passe, err := o.lance(context.Background(), sourceFournie,
		corpusRepete("1 pincée de sel", 100))
	if err == nil {
		t.Fatal("analyse menée à son terme, attendu un dépassement de délai")
	}
	if !strings.Contains(err.Error(), delaiDUneAnalyse.String()) {
		t.Errorf("message = %q, attendu qu'il donne le délai %s", err, delaiDUneAnalyse)
	}
	if statut := passeRelue(t, app, passe).GetString("status"); statut != statutEchec {
		t.Errorf("status = %q, attendu %q", statut, statutEchec)
	}
}

// --- La reprise au démarrage ------------------------------------------------

// Une analyse laissée en cours par un arrêt repart en échec, et non à faire :
// le corpus d'un travail fourni n'existe plus au redémarrage, une reprise le
// rejouerait à vide indéfiniment. C'est l'inverse de l'ouvrier d'import.
func TestUneAnalyseInterrompueRepartEnEchec(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	interrompue := passeEnCours(t, app)

	if err := o.rendLesAnalysesInterrompues(); err != nil {
		t.Fatalf("reprise : %v", err)
	}

	relue := passeRelue(t, app, interrompue)
	if statut := relue.GetString("status"); statut != statutEchec {
		t.Errorf("status = %q, attendu %q — une passe éternellement en cours n'est pas lisible",
			statut, statutEchec)
	}
	if relue.GetDateTime("finished").IsZero() {
		t.Error("finished est vide : une passe close sans date ne se date plus")
	}

	// Et la file est rendue : une analyse peut repartir.
	if _, err := o.lance(context.Background(), sourceFournie, corpus("2 tomates")); err != nil {
		t.Errorf("analyse refusée après la reprise : %v", err)
	}
}

// passeEnCours écrit la passe qu'un arrêt aurait laissée derrière lui.
func passeEnCours(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("analyses")
	if err != nil {
		t.Fatalf("collection analyses : %v", err)
	}
	passe := core.NewRecord(collection)
	passe.Set("source", sourceFournie)
	passe.Set("status", statutEnCours)
	if err := app.Save(passe); err != nil {
		t.Fatalf("enregistrement de la passe en cours : %v", err)
	}
	return passe
}

// --- La source « base de l'instance » ---------------------------------------

// L'établi lit le carnet, il ne l'abîme pas : la promesse tient sur les deux
// collections que la lecture traverse.
func TestLAnalyseDeLInstanceNEcritNiIngredientsNiRecettes(t *testing.T) {
	app, _, o := atelierDAnalyse(t, horlogeFigee())

	recette := recetteNeuve(t, app)
	for _, brut := range []string{"100 g de farine", "1 pincée de sel", "100 g de farine"} {
		ligne := ingredientNeuf(t, app, recette, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}

	avant := empreinteDes(t, app, "ingredients", "recipes")

	passe, err := o.lance(context.Background(), sourceInstance, lignesDeLInstance(app))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	if apres := empreinteDes(t, app, "ingredients", "recipes"); apres != avant {
		t.Errorf("les collections ont bougé :\navant : %s\naprès : %s", avant, apres)
	}

	relue := passeRelue(t, app, passe)
	if lues := relue.GetInt("lines"); lues != 3 {
		t.Errorf("lines = %d, attendu 3 — les trois lignes de la base", lues)
	}
	if formes := relue.GetInt("forms"); formes != 2 {
		t.Errorf("forms = %d, attendu 2 — « 100 g de farine » est écrite deux fois", formes)
	}
	if source := relue.GetString("source"); source != sourceInstance {
		t.Errorf("source = %q, attendu %q", source, sourceInstance)
	}
}

// empreinteDes rend le contenu des collections, enregistrement par
// enregistrement : c'est ce qui se compare avant et après.
func empreinteDes(t *testing.T, app core.App, collections ...string) string {
	t.Helper()

	var empreinte strings.Builder
	for _, nom := range collections {
		enregistrements, err := app.FindAllRecords(nom)
		if err != nil {
			t.Fatalf("lecture de %s : %v", nom, err)
		}
		fmt.Fprintf(&empreinte, "%s (%d)\n", nom, len(enregistrements))
		for _, enregistrement := range enregistrements {
			fmt.Fprintf(&empreinte, "  %s %v\n", enregistrement.Id, enregistrement.FieldsData())
		}
	}
	return empreinte.String()
}

// --- L'avancement -----------------------------------------------------------

// Trois cent vingt mille mises à jour coûteraient plus cher que l'analyse
// elle-même : l'avancement se pousse au plus une fois par seconde, plus une
// fois à la fin. Le compte se borne par la durée, jamais par le nombre de
// lignes.
func TestLAvancementSeBorneParLaDureeEtNonParLesLignes(t *testing.T) {
	const lignes = 10_000
	// Une milliseconde par lecture de l'horloge : le corpus dure une dizaine
	// de secondes de temps réglé, et le délai de trente minutes reste loin.
	horloge := horlogeAuPas(time.Millisecond)
	app, _, o := atelierDAnalyse(t, horloge)

	var ecritures atomic.Int64
	app.OnRecordUpdate("analyses").BindFunc(func(e *core.RecordEvent) error {
		ecritures.Add(1)
		return e.Next()
	})

	if _, err := o.lance(context.Background(), sourceFournie,
		corpusRepete("1 pincée de sel", lignes)); err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	// La durée réglée de la passe borne le compte : une écriture par seconde
	// écoulée, plus celle de la clôture, plus une marge d'arrondi.
	ecoule := horloge.Maintenant().Sub(departDesTests)
	plafond := int64(ecoule/cadenceDeLAvancement) + 2
	if obtenues := ecritures.Load(); obtenues > plafond {
		t.Errorf("%d écriture(s) sur analyses pour %v de passe, attendu au plus %d",
			obtenues, ecoule, plafond)
	}
	// Et le garde-fou du garde-fou : le compte ne suit pas les lignes.
	if obtenues := ecritures.Load(); obtenues >= lignes {
		t.Errorf("%d écriture(s) pour %d lignes : l'avancement s'écrit ligne à ligne",
			obtenues, lignes)
	}
}

// --- Le démarrage et l'arrêt ------------------------------------------------

// TestLOuvrierDAnalyseDemarreAvecLeServeurEtSArreteAvecLui :
// brancheLOuvrierDAnalyse est le seul chemin par lequel l'ouvrier tourne en
// production. Sans ce test, retirer son appel de main(), ou vider le corps de
// tourne, laisserait la suite entièrement verte — le même argument que
// TestLOuvrierDemarreAvecLeServeurEtSArreteAvecLui pour l'ouvrier d'import.
//
// Les trois temps que lui seul prouve : la reprise en échec passe bien par le
// démarrage — les autres tests appellent rendLesAnalysesInterrompues à la
// main —, une passe déposée dans la file est menée à son terme — c'est le seul
// chemin que la page de PATA-124 empruntera —, et l'arrêt attend l'ouvrier.
// Cette attente est ce qui garantit que la dernière écriture, la passe en
// cours qui passe en échec, se fait sur une base encore ouverte : c'est l'état
// que la reprise du démarrage suivant attend.
func TestLOuvrierDAnalyseDemarreAvecLeServeurEtSArreteAvecLui(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)

	interrompue := passeEnCours(t, app)

	o := brancheLOuvrierDAnalyse(app, a)
	// Un test qui échoue avant l'arrêt laisserait l'ouvrier tourner sur une
	// base que le nettoyage referme. Arrêter deux fois est sans effet.
	t.Cleanup(func() { _ = app.OnTerminate().Trigger(&core.TerminateEvent{App: app}) })

	if err := app.OnServe().Trigger(&core.ServeEvent{App: app}); err != nil {
		t.Fatalf("démarrage du serveur : %v", err)
	}

	// Le démarrage : la passe qu'un arrêt a laissée en cours est rendue à
	// l'échec sans que personne ne l'ait demandé.
	attendLeStatut(t, app, interrompue, statutEchec,
		"la passe laissée en cours n'a pas été reprise au démarrage")

	// La file : une passe déposée est menée, avec ses formes écrites.
	deposee, err := o.metEnFile(sourceFournie,
		corpus("100 g de farine", "1 pincée de sel", "100 g de farine"))
	if err != nil {
		t.Fatalf("mise en file refusée : %v", err)
	}
	menee := attendLeStatut(t, app, deposee, statutTermine,
		"la passe déposée dans la file n'a pas été menée : l'ouvrier ne la consomme pas")
	if formes := menee.GetInt("forms"); formes != 2 {
		t.Errorf("forms = %d, attendu 2 — « 100 g de farine » est déposée deux fois", formes)
	}
	if ecrites := formesDe(t, app, deposee); len(ecrites) != 2 {
		t.Errorf("%d forme(s) écrite(s), attendu 2 : la passe a été close sans être analysée",
			len(ecrites))
	}

	// L'arrêt : OnTerminate ne rend la main qu'une fois la passe en cours
	// close, sur une base encore ouverte.
	partie := make(chan struct{}, 1)
	interminable, err := o.metEnFile(sourceFournie, corpusQuiTraine(partie))
	if err != nil {
		t.Fatalf("mise en file de la passe interminable refusée : %v", err)
	}
	select {
	case <-partie:
	case <-time.After(10 * time.Second):
		t.Fatal("aucune ligne n'a été lue : l'ouvrier n'a pas pris la passe")
	}

	if err := app.OnTerminate().Trigger(&core.TerminateEvent{App: app}); err != nil {
		t.Fatalf("arrêt du serveur : %v", err)
	}
	if statut := passeRelue(t, app, interminable).GetString("status"); statut != statutEchec {
		t.Errorf("passe en %q quand OnTerminate a rendu la main, attendu %q : "+
			"l'arrêt n'a pas attendu l'ouvrier", statut, statutEchec)
	}
}

// corpusQuiTraine rend des lignes sans jamais s'arrêter, en signalant la
// première : c'est ce qui laisse au test le temps de déclencher l'arrêt
// pendant qu'une passe tourne.
//
// La pause entre deux lignes est ce qui rend l'attente d'OnTerminate visible :
// l'annulation du contexte tombe pendant l'une d'elles, et l'ouvrier ne clôt
// donc la passe qu'après. Sans attente, le test lirait la base avant cette
// écriture-là.
func corpusQuiTraine(partie chan<- struct{}) sourceDeLignes {
	return func(yield func(string, error) bool) {
		for {
			if !yield("1 pincée de sel", nil) {
				return
			}
			select {
			case partie <- struct{}{}:
			default:
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// attendLeStatut attend que la passe porte le statut voulu, et la rend relue.
// L'ouvrier tourne sur sa propre goroutine : un test qui lirait la base
// aussitôt y lirait l'état d'avant.
func attendLeStatut(t *testing.T, app core.App, passe *core.Record,
	statut, plainte string) *core.Record {
	t.Helper()

	limite := time.Now().Add(10 * time.Second)
	for {
		relue := passeRelue(t, app, passe)
		obtenu := relue.GetString("status")
		if obtenu == statut {
			return relue
		}
		if time.Now().After(limite) {
			t.Fatalf("status = %q après dix secondes, attendu %q : %s", obtenu, statut, plainte)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
