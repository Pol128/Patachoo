package main

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// La comparaison de deux lectures du même corpus : ce qui dit si le parser lit
// mieux qu'avant, sans vérité de référence.
//
// Les passes de ces tests sont écrites de toutes pièces — un enregistrement
// d'analyse et ses formes, avec les signaux qu'on leur choisit. C'est ce qui
// permet d'éprouver la comparaison sur des écarts décidés plutôt que sur ce
// que le parser du jour rend : le jour où le moteur monte de version, ces
// tests parlent encore de la comparaison et non du moteur.

// --- Montage ----------------------------------------------------------------

// formeVoulue est une forme telle qu'un test la dépose : sa lecture, ses
// signaux, son motif, et si son aliment se retrouve au lexique.
type formeVoulue struct {
	brut    string
	lecture lectureDUneForme
	signaux []string
	motif   string
	resolu  bool
}

// passeConstruite écrit une passe terminée et les formes qu'on lui donne.
func passeConstruite(t *testing.T, app core.App, formes ...formeVoulue) *core.Record {
	t.Helper()

	collectionDesPasses, err := app.FindCollectionByNameOrId("analyses")
	if err != nil {
		t.Fatalf("collection analyses : %v", err)
	}
	passe := core.NewRecord(collectionDesPasses)
	passe.Set("source", sourceFournie)
	passe.Set("status", statutTermine)
	passe.Set("lines", len(formes))
	passe.Set("forms", len(formes))
	if err := app.Save(passe); err != nil {
		t.Fatalf("écriture de la passe : %v", err)
	}

	collectionDesFormes, err := app.FindCollectionByNameOrId("analyses_formes")
	if err != nil {
		t.Fatalf("collection analyses_formes : %v", err)
	}
	for _, voulue := range formes {
		forme := core.NewRecord(collectionDesFormes)
		forme.Set("analysis", passe.Id)
		forme.Set("raw", voulue.brut)
		forme.Set("occurrences", 1)
		forme.Set("pattern", voulue.motif)
		forme.Set("reading", voulue.lecture)
		forme.Set("food", voulue.lecture.Aliment)
		forme.Set("resolved", voulue.resolu)
		// Jamais nil : la colonne part en JSON, et json_array_length veut un
		// tableau des deux côtés.
		if voulue.signaux == nil {
			voulue.signaux = []string{}
		}
		forme.Set("signals", voulue.signaux)
		if err := app.Save(forme); err != nil {
			t.Fatalf("écriture de la forme %q : %v", voulue.brut, err)
		}
	}
	return passe
}

// quantiteDe rend le pointeur qu'une lecture porte : nil vaut « pas de
// quantité », et c'est un cas de la comparaison, pas un détail.
func quantiteDe(v float64) *float64 { return &v }

// comparaisonDeDeuxPasses monte une base et deux passes, et rend la sortie de
// la sous-commande.
func comparaisonDeDeuxPasses(t *testing.T, app core.App, avant, apres *core.Record) string {
	t.Helper()

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "comparer", avant.Id, apres.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}
	return sortie
}

// exigeLesLignes vérifie que la sortie porte chacune des mesures données.
func exigeLesLignes(t *testing.T, sortie string, mesures ...string) {
	t.Helper()

	for _, mesure := range mesures {
		if !strings.Contains(sortie, mesure) {
			t.Errorf("sortie sans « %s » :\n%s", mesure, sortie)
		}
	}
}

// --- Le compte des formes sans signal ---------------------------------------

// Le chiffre principal du verdict : combien de formes n'allument aucun signal.
// Les comptes par signal ne le donnent pas — une forme peut en allumer deux.
//
// Et l'invariant qui le rend lisible : ce nombre plus celui des formes qui
// allument au moins un signal fait le total des formes.
func TestLeResumeCompteLesFormesSansSignal(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)
	o := nouvelOuvrierDAnalyse(app, horlogeFigee(), a)

	passe, err := o.lance(context.Background(), sourceFournie, corpus(
		"1 pincée de sel",      // résolue, aucun signal
		"2 tomates",            // résolue, aucun signal
		"100 g de farine",      // non résolue
		"1 feuille de laurier", // résolue, unité répétée
	))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	mesures, err := resumeDe(app, passeRelue(t, app, passe))
	if err != nil {
		t.Fatalf("résumé refusé : %v", err)
	}
	if mesures.sansSignal != 2 {
		t.Errorf("formes sans signal = %d, attendu 2", mesures.sansSignal)
	}
	if mesures.sansSignal+mesures.avecSignal != mesures.formes {
		t.Errorf("%d sans signal + %d avec au moins un = %d, attendu %d formes",
			mesures.sansSignal, mesures.avecSignal,
			mesures.sansSignal+mesures.avecSignal, mesures.formes)
	}

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "resume", passe.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}
	exigeLesLignes(t, sortie, "formes sans signal : 2")
}

// --- Les quatre quantités ---------------------------------------------------

// Ce que la comparaison rend d'abord : les quatre quantités de part et
// d'autre, avec leur écart. Le compte des formes sans signal, le taux de
// résolution, le compte de chaque signal clé par clé, et le compte par motif —
// ce dernier étant ce qui dit si les règles promises par le pack ont bien été
// livrées.
func TestComparerRendLesQuatreQuantitesDePartEtDAutre(t *testing.T) {
	app := baseNeuve(t)

	avant := passeConstruite(t, app,
		formeVoulue{
			brut:    "100 g de farine de sarrasin",
			lecture: lectureDUneForme{Quantite: quantiteDe(100), Unite: "g", Partitif: "de", Aliment: "farine de sarrasin"},
			signaux: []string{SignalNonResolu},
			motif:   "quantite_unite_partitif",
		},
		formeVoulue{
			brut:    "2 tomates",
			lecture: lectureDUneForme{Quantite: quantiteDe(2), Aliment: "tomate"},
			motif:   "quantite_aliment",
			resolu:  true,
		},
		formeVoulue{
			brut:    "1 feuille de laurier, ciselée",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "feuille", Partitif: "de", Aliment: "feuille de laurier, ciselée"},
			signaux: []string{SignalMotsPerdus, SignalUniteRepetee},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "sel",
			lecture: lectureDUneForme{Aliment: "sel"},
			motif:   "aliment_nu",
			resolu:  true,
		},
	)
	apres := passeConstruite(t, app,
		// Le lexique la résout désormais, et son aliment prend la forme
		// canonique : un signal de moins, et le seul champ qui bouge est food.
		// Ses signaux ayant bougé, elle est à relire et non comptée en
		// canonisation — ce que ce test-ci ne regarde pas, il compte.
		formeVoulue{
			brut:    "100 g de farine de sarrasin",
			lecture: lectureDUneForme{Quantite: quantiteDe(100), Unite: "g", Partitif: "de", Aliment: "farine de sarrasin bio"},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "2 tomates",
			lecture: lectureDUneForme{Quantite: quantiteDe(2), Aliment: "tomate"},
			motif:   "quantite_aliment",
			resolu:  true,
		},
		// La préparation part en note : un signal de moins, un motif qui change.
		formeVoulue{
			brut:    "1 feuille de laurier, ciselée",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "feuille", Partitif: "de", Aliment: "feuille de laurier", Note: "ciselée"},
			signaux: []string{SignalUniteRepetee},
			motif:   "quantite_unite_note",
			resolu:  true,
		},
		formeVoulue{
			brut:    "sel",
			lecture: lectureDUneForme{Aliment: "sel"},
			motif:   "aliment_nu",
			resolu:  true,
		},
	)

	sortie := comparaisonDeDeuxPasses(t, app, avant, apres)

	exigeLesLignes(t, sortie,
		"formes communes : 4",
		// 1. le chiffre principal
		"formes sans signal : avant 2, après 3, écart +1",
		// 2. le taux de résolution, et le compte dont il sort
		"résolus : avant 3, après 4, écart +1",
		"taux de résolution : avant 75,0 %, après 100,0 %, écart +25,0 points",
		// 3. le compte de chaque signal, clé par clé — union des deux côtés
		"signal "+SignalMotsPerdus+" : avant 1, après 0, écart -1",
		"signal "+SignalNonResolu+" : avant 1, après 0, écart -1",
		"signal "+SignalUniteRepetee+" : avant 1, après 1, écart 0",
		// 4. le compte par motif : ce qui dit si la règle promise a été livrée
		"motif quantite_unite_partitif : avant 2, après 1, écart -1",
		"motif quantite_unite_note : avant 0, après 1, écart +1",
		"motif aliment_nu : avant 1, après 1, écart 0",
	)
}

// --- La règle sans tolérance ------------------------------------------------

// Une forme qui n'allumait rien et qui allume maintenant quelque chose est une
// régression, quelle que soit l'évolution du solde — et elle est listée en
// entier, avec son brut et ses deux lectures, parce que c'est le seul endroit
// où un humain est requis.
//
// L'autre sens, dans le même test : une forme qui passe de deux signaux à un
// n'est pas une régression. Sans lui, un « toute forme qui change est une
// régression » passerait.
func TestUneFormePasseeDeZeroSignalAUnEstUneRegression(t *testing.T) {
	app := baseNeuve(t)

	avant := passeConstruite(t, app,
		formeVoulue{
			brut:    "1 bouquet garni",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Aliment: "bouquet garni"},
			motif:   "aliment_compose",
			resolu:  true,
		},
		formeVoulue{
			brut:    "2 gousses d'ail",
			lecture: lectureDUneForme{Quantite: quantiteDe(2), Unite: "gousses", Partitif: "d'", Aliment: "gousse d'ail"},
			signaux: []string{SignalMotsPerdus, SignalUniteRepetee},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
	)
	apres := passeConstruite(t, app,
		// La régression : plus rien ne la résout, et l'aliment ressort vide.
		formeVoulue{
			brut:    "1 bouquet garni",
			lecture: lectureDUneForme{Quantite: quantiteDe(1)},
			signaux: []string{SignalAlimentVide, SignalMotsPerdus},
			motif:   "quantite_seule",
		},
		// Deux signaux, puis un : une amélioration, pas une régression.
		formeVoulue{
			brut:    "2 gousses d'ail",
			lecture: lectureDUneForme{Quantite: quantiteDe(2), Unite: "gousses", Partitif: "d'", Aliment: "ail"},
			signaux: []string{SignalMotsPerdus},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
	)

	sortie := comparaisonDeDeuxPasses(t, app, avant, apres)

	exigeLesLignes(t, sortie,
		"régressions : 1",
		"régression « 1 bouquet garni »",
		// Les deux lectures, pour être relues : celle d'avant porte l'aliment,
		// celle d'après ne le porte plus.
		`"food":"bouquet garni"`,
		`"signals":["`+SignalAlimentVide+`","`+SignalMotsPerdus+`"]`,
	)
	if strings.Contains(sortie, "régression « 2 gousses d'ail »") {
		t.Errorf("la forme passée de deux signaux à un est comptée en régression :\n%s", sortie)
	}
}

// --- La canonisation de l'aliment -------------------------------------------

// MOTEUR-5 réécrit food avec la forme canonique du lexique sur toute ligne qui
// se résout : la moitié du corpus change sans qu'aucune erreur soit réparée.
// Cette famille est comptée à part, nommée comme telle, et tenue hors du
// verdict — sans quoi tout décompte de formes changées serait ininterprétable.
func TestUneCanonisationDeLAlimentEstCompteeAPart(t *testing.T) {
	app := baseNeuve(t)

	avant := passeConstruite(t, app, formeVoulue{
		brut:    "20 cl de crème fraîche épaisse",
		lecture: lectureDUneForme{Quantite: quantiteDe(20), Unite: "cl", Partitif: "de", Aliment: "crème fraîche épaisse"},
		motif:   "quantite_unite_partitif",
		resolu:  true,
	})
	apres := passeConstruite(t, app, formeVoulue{
		brut:    "20 cl de crème fraîche épaisse",
		lecture: lectureDUneForme{Quantite: quantiteDe(20), Unite: "cl", Partitif: "de", Aliment: "crème fraîche"},
		motif:   "quantite_unite_partitif",
		resolu:  true,
	})

	sortie := comparaisonDeDeuxPasses(t, app, avant, apres)

	exigeLesLignes(t, sortie,
		"canonisation de l'aliment : 1 (hors verdict)",
		"régressions : 0",
		"à relire : 0",
		// Hors du verdict : aucune des quantités ne bouge sous son effet.
		"formes sans signal : avant 1, après 1, écart 0",
		"taux de résolution : avant 100,0 %, après 100,0 %, écart +0,0 points",
	)
	if strings.Contains(sortie, "régression « 20 cl de crème fraîche épaisse »") {
		t.Errorf("une canonisation d'aliment est comptée en régression :\n%s", sortie)
	}
}

// Un changement du seul champ food n'est pas une canonisation pour autant. La
// famille ne vaut que pour ce qu'elle nomme — MOTEUR-5 réécrit l'aliment avec
// la forme canonique du lexique sur une ligne qui se résout, « sans qu'aucune
// erreur soit réparée ». Une ligne dont l'aliment se vide, cesse de se
// résoudre, ou allume un signal de plus a bien changé de la seule colonne
// food, et n'en est pas moins une dégradation : la ranger en canonisation la
// tiendrait hors du verdict sans jamais la lister, ce que l'invariant 4 de la
// tâche interdit.
//
// Trois gardes, donc, et une forme par garde — chacune ne tombe que sur celle
// qu'elle éprouve, pour qu'en retirer une fasse rougir un cas et un seul. La
// quatrième forme est le témoin : une vraie canonisation reste comptée comme
// telle.
func TestUnAlimentQuiSeDegradeNEstPasUneCanonisation(t *testing.T) {
	app := baseNeuve(t)

	avant := passeConstruite(t, app,
		// Le témoin.
		formeVoulue{
			brut:    "3 tomates",
			lecture: lectureDUneForme{Quantite: quantiteDe(3), Aliment: "tomates"},
			motif:   "quantite_aliment",
			resolu:  true,
		},
		// La garde des signaux : l'aliment d'après est non vide et résolu, mais
		// la lecture allume un signal de plus qu'avant.
		formeVoulue{
			brut:    "100 g de farine de blé",
			lecture: lectureDUneForme{Quantite: quantiteDe(100), Unite: "g", Partitif: "de", Aliment: "farine de blé"},
			signaux: []string{SignalMotsPerdus},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		// La garde de l'aliment vide : rien d'autre ne bouge, signaux compris.
		formeVoulue{
			brut:    "20 cl de crème",
			lecture: lectureDUneForme{Quantite: quantiteDe(20), Unite: "cl", Partitif: "de", Aliment: "crème"},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		// La garde de la résolution : l'aliment d'après est bien rempli, mais
		// le lexique ne le retrouve plus.
		formeVoulue{
			brut:    "1 pincée de sel",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "pincée", Partitif: "de", Aliment: "sel"},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
	)
	apres := passeConstruite(t, app,
		formeVoulue{
			brut:    "3 tomates",
			lecture: lectureDUneForme{Quantite: quantiteDe(3), Aliment: "tomate"},
			motif:   "quantite_aliment",
			resolu:  true,
		},
		formeVoulue{
			brut:    "100 g de farine de blé",
			lecture: lectureDUneForme{Quantite: quantiteDe(100), Unite: "g", Partitif: "de", Aliment: "farine"},
			signaux: []string{SignalMotsPerdus, SignalUniteRepetee},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "20 cl de crème",
			lecture: lectureDUneForme{Quantite: quantiteDe(20), Unite: "cl", Partitif: "de"},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "1 pincée de sel",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "pincée", Partitif: "de", Aliment: "gros sel"},
			motif:   "quantite_unite_partitif",
		},
	)

	sortie := comparaisonDeDeuxPasses(t, app, avant, apres)

	exigeLesLignes(t, sortie,
		// Le témoin, et lui seul.
		"canonisation de l'aliment : 1 (hors verdict)",
		// Les trois autres sont listées en entier, pour être lues.
		"à relire : 3",
		"à relire « 100 g de farine de blé »",
		"à relire « 20 cl de crème »",
		"à relire « 1 pincée de sel »",
		// Et l'aliment perdu se lit dans les deux lectures de l'écart.
		`"food":"crème"`,
		`"food":""`,
	)
	if strings.Contains(sortie, "à relire « 3 tomates »") {
		t.Errorf("le témoin de canonisation est passé en « à relire » :\n%s", sortie)
	}
}

// La même règle en --base, et c'est le mode qui compte : perimetreDeLaBase ne
// porte pas les signaux, la règle sans tolérance ne s'y arme donc jamais et
// aucun compte par signal n'y est imprimé. Une ligne dont l'aliment se perd
// entre les colonnes d'ingredients et une passe ne laisserait, rangée en
// canonisation, aucune trace nulle part — dans le mode même qui doit trancher
// la reprise de l'existant.
func TestComparerLaBaseNeRangePasUnAlimentPerduEnCanonisation(t *testing.T) {
	app := baseNeuve(t)

	recette := recetteNeuve(t, app)
	// L'aliment que la base porte disparaît de la passe : seul food bouge, et
	// ce n'est pas une canonisation.
	ligneDeBaseImposee(t, app, recette, "200 g de farine",
		lectureDUneForme{Quantite: quantiteDe(200), Unite: "g", Aliment: "farine"})
	// Le témoin : une vraie canonisation reste comptée comme telle.
	ligneDeBaseImposee(t, app, recette, "3 pommes",
		lectureDUneForme{Quantite: quantiteDe(3), Aliment: "pommes"})

	passe := passeConstruite(t, app,
		formeVoulue{
			brut:    "200 g de farine",
			lecture: lectureDUneForme{Quantite: quantiteDe(200), Unite: "g", Partitif: "de"},
			signaux: []string{SignalAlimentVide},
			motif:   "quantite_unite_partitif",
		},
		formeVoulue{
			brut:    "3 pommes",
			lecture: lectureDUneForme{Quantite: quantiteDe(3), Aliment: "pomme"},
			motif:   "quantite_aliment",
			resolu:  true,
		},
	)

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "comparer", "--base", passe.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	exigeLesLignes(t, sortie,
		"formes communes : 2",
		"canonisation de l'aliment : 1 (hors verdict)",
		"à relire : 1",
		"à relire « 200 g de farine »",
		`"food":"farine"`,
		`"food":""`,
	)
	if strings.Contains(sortie, "à relire « 3 pommes »") {
		t.Errorf("le témoin de canonisation est passé en « à relire » :\n%s", sortie)
	}
}

// ligneDeBaseImposee écrit une ligne de l'instance et lui impose ses champs
// dérivés.
//
// Le hook les remplit depuis raw à la création ; une seconde écriture, raw
// inchangé, ne les recalcule pas — c'est ce qui fait survivre une correction
// manuelle, et c'est ce qui permet ici de choisir l'« avant » plutôt que de
// subir la lecture du parser du jour. Ces tests-là parlent de la comparaison,
// pas du moteur.
func ligneDeBaseImposee(t *testing.T, app core.App, recette *core.Record,
	brut string, lue lectureDUneForme) {
	t.Helper()

	ligne := ingredientNeuf(t, app, recette, brut)
	if err := app.Save(ligne); err != nil {
		t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
	}

	// Relue depuis la base : c'est cette lecture que Original() rend au hook,
	// et c'est elle qui lui dit que raw n'a pas bougé.
	imposee := relit(t, app, ligne.Id)
	var quantite float64
	if lue.Quantite != nil {
		quantite = *lue.Quantite
	}
	imposee.Set("quantity", quantite)
	imposee.Set("unit", lue.Unite)
	imposee.Set("food", lue.Aliment)
	imposee.Set("note", lue.Note)
	imposee.Set("optional", lue.Optionnel)
	if err := app.Save(imposee); err != nil {
		t.Fatalf("champs imposés à la ligne %q : %v", brut, err)
	}
}

// --- Le corpus qui a bougé --------------------------------------------------

// Le rapprochement se fait sur raw, qui ne bouge jamais d'une passe à l'autre.
// Une forme présente d'un seul côté ne dit rien du parser, seulement que le
// corpus a bougé : elle est comptée à part, et elle n'entre dans aucun écart.
func TestUneFormePresenteDUnSeulCoteEstCompteeAPart(t *testing.T) {
	app := baseNeuve(t)

	commune := formeVoulue{
		brut:    "3 pommes",
		lecture: lectureDUneForme{Quantite: quantiteDe(3), Aliment: "pomme"},
		motif:   "quantite_aliment",
		resolu:  true,
	}
	avant := passeConstruite(t, app, commune, formeVoulue{
		brut:    "1 pincée de sel",
		lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "pincée", Partitif: "de", Aliment: "sel"},
		motif:   "quantite_unite_partitif",
		resolu:  true,
	})
	apres := passeConstruite(t, app, commune, formeVoulue{
		brut:    "un peu de poivre du moulin",
		lecture: lectureDUneForme{Partitif: "de"},
		signaux: []string{SignalAlimentVide, SignalMotsPerdus, SignalNonResolu},
		motif:   "quantite_indefinie",
	})

	sortie := comparaisonDeDeuxPasses(t, app, avant, apres)

	exigeLesLignes(t, sortie,
		"formes communes : 1",
		"formes seulement avant : 1",
		"formes seulement après : 1",
		// Les trois signaux de la forme qui n'existe que d'un côté ne pèsent
		// sur aucun écart.
		"formes sans signal : avant 1, après 1, écart 0",
		"régressions : 0",
		"à relire : 0",
		"formes identiques : 1",
	)
	if strings.Contains(sortie, "signal "+SignalAlimentVide) {
		t.Errorf("une forme présente d'un seul côté compte dans un écart :\n%s", sortie)
	}
}

// --- La mesure historique, contre les colonnes de la base -------------------

// Le premier « avant » n'est pas une passe : Patachoo n'embarque qu'un parser à
// la fois, et on ne peut pas rejouer la version qui a écrit la base. Le
// rapprochement se fait donc entre les colonnes d'ingredients et une passe, sur
// raw.
//
// unit est hors de cette comparaison-là, et de celle-ci seulement : la colonne
// porte l'abréviation canonique (« cs »), la lecture d'une passe garde le texte
// de la ligne (« cuillères à soupe »), et les deux sont voulus. Comparé, ce
// champ dirait « changé » sur toute ligne parfaitement lue.
func TestComparerLaBaseRapprocheSurRawSansComparerLUnite(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)

	recette := recetteNeuve(t, app)
	for _, brut := range []string{"2 cuillères à soupe de sucre", "sel", "3 pommes"} {
		ligne := ingredientNeuf(t, app, recette, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}

	o := nouvelOuvrierDAnalyse(app, horlogeFigee(), a)
	passe, err := o.lance(context.Background(), sourceInstance, lignesDeLInstance(app))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	// Le garde-fou du test : sans écart sur unit, il ne prouverait rien.
	lignes, err := app.FindAllRecords("ingredients")
	if err != nil {
		t.Fatalf("lecture des ingrédients : %v", err)
	}
	var uniteEnBase string
	for _, ligne := range lignes {
		if ligne.GetString("raw") == "2 cuillères à soupe de sucre" {
			uniteEnBase = ligne.GetString("unit")
		}
	}
	forme := formesDe(t, app, passe)["2 cuillères à soupe de sucre"]
	if forme == nil {
		t.Fatal("la passe n'a pas de forme pour « 2 cuillères à soupe de sucre »")
	}
	var lue lectureDUneForme
	if err := forme.UnmarshalJSONField("reading", &lue); err != nil {
		t.Fatalf("lecture de la forme : %v", err)
	}
	if uniteEnBase == "" || uniteEnBase == lue.Unite {
		t.Fatalf("unit en base %q et dans la passe %q : le test ne prouve rien sans écart",
			uniteEnBase, lue.Unite)
	}

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "comparer", "--base", passe.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	exigeLesLignes(t, sortie,
		"avant : les colonnes d'ingredients",
		"formes communes : 3",
		"formes seulement avant : 0",
		"formes seulement après : 0",
		"champs hors comparaison : unit",
		// Les trois lignes sont lues par le même parser des deux côtés : à unit
		// près, tout est identique.
		"formes identiques : 3",
		"à relire : 0",
		"canonisation de l'aliment : 0",
	)
}

// L'établi lit le carnet, il ne le modifie pas — et la comparaison moins que
// tout le reste : c'est ce qu'un exploitant doit pouvoir tenir pour acquis
// avant de la lancer sur sa base. Le relevé porte sur les deux collections,
// champ à champ.
func TestComparerLaBaseNEcritNiDansIngredientsNiDansRecipes(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)

	recette := recetteNeuve(t, app)
	for _, brut := range []string{"200 g de farine", "3 pommes"} {
		ligne := ingredientNeuf(t, app, recette, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}

	o := nouvelOuvrierDAnalyse(app, horlogeFigee(), a)
	passe, err := o.lance(context.Background(), sourceInstance, lignesDeLInstance(app))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	avant := releveDesCollections(t, app, "recipes", "ingredients")

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "comparer", "--base", passe.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	if apres := releveDesCollections(t, app, "recipes", "ingredients"); apres != avant {
		t.Errorf("le carnet a changé pendant la comparaison.\navant :\n%s\naprès :\n%s", avant, apres)
	}
}

// --- La sortie se diffe -----------------------------------------------------

// La sortie est faite pour être comparée d'une exécution à l'autre : deux
// exécutions sur les mêmes passes rendent le même texte, à l'octet. Sans tri,
// le parcours des tables de hachage des signaux et des motifs suffirait à la
// faire varier.
func TestLaSortieDeComparerEstIdentiqueDUneExecutionALAutre(t *testing.T) {
	app := baseNeuve(t)

	avant := passeConstruite(t, app,
		formeVoulue{
			brut:    "100 g de farine",
			lecture: lectureDUneForme{Quantite: quantiteDe(100), Unite: "g", Partitif: "de", Aliment: "farine"},
			signaux: []string{SignalNonResolu},
			motif:   "quantite_unite_partitif",
		},
		formeVoulue{
			brut:    "1 feuille de laurier",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "feuille", Partitif: "de", Aliment: "feuille de laurier"},
			signaux: []string{SignalMotsPerdus, SignalUniteRepetee},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "Aubergines : 500 g",
			lecture: lectureDUneForme{Aliment: "Aubergines : 500 g"},
			signaux: []string{SignalNonResolu},
			motif:   "aliment_nu",
		},
		formeVoulue{
			brut:    "3 tomates mondées, épépinées",
			lecture: lectureDUneForme{Quantite: quantiteDe(3), Aliment: "tomates mondées, épépinées"},
			signaux: []string{SignalNonResolu},
			motif:   "quantite_aliment",
		},
	)
	apres := passeConstruite(t, app,
		formeVoulue{
			brut:    "100 g de farine",
			lecture: lectureDUneForme{Quantite: quantiteDe(100), Unite: "g", Partitif: "de", Aliment: "farine"},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "1 feuille de laurier",
			lecture: lectureDUneForme{Quantite: quantiteDe(1), Unite: "feuille", Partitif: "de", Aliment: "laurier"},
			signaux: []string{SignalAlimentVide},
			motif:   "quantite_unite_partitif",
			resolu:  true,
		},
		formeVoulue{
			brut:    "Aubergines : 500 g",
			lecture: lectureDUneForme{Quantite: quantiteDe(500), Unite: "g", Aliment: "aubergine"},
			motif:   "aliment_quantite",
			resolu:  true,
		},
		formeVoulue{
			brut:    "3 tomates mondées, épépinées",
			lecture: lectureDUneForme{Quantite: quantiteDe(3), Aliment: "tomate", Note: "épépinées"},
			motif:   "quantite_aliment_note",
			resolu:  true,
		},
	)

	premiere := comparaisonDeDeuxPasses(t, app, avant, apres)
	seconde := comparaisonDeDeuxPasses(t, app, avant, apres)
	if premiere != seconde {
		t.Errorf("deux exécutions sur les mêmes passes rendent deux textes.\npremière :\n%s\nseconde :\n%s",
			premiere, seconde)
	}

	// Et la forme vérifiable de la même exigence : les suites sont triées.
	// Deux exécutions qui s'accordent peuvent s'accorder par chance — le
	// parcours d'une table de hachage de trois clés ressort dans le même ordre
	// une fois sur six.
	exigeCroissant(t, premiere, "signal ", 4)
	exigeCroissant(t, premiere, "motif ", 5)
	// Quatre, et non trois : « 1 feuille de laurier » perd son aliment au
	// profit d'un autre en allumant aliment_vide. Seul food bouge, mais ses
	// signaux aussi — ce n'est donc pas une canonisation, et elle est listée.
	exigeCroissant(t, premiere, "à relire « ", 4)
}

// exigeCroissant relève les lignes de la sortie qui commencent par le préfixe
// donné et vérifie qu'elles sont triées. Le compte attendu est passé avec :
// une suite vide est triée, et ne prouverait donc rien.
func exigeCroissant(t *testing.T, sortie, prefixe string, attendues int) {
	t.Helper()

	var relevees []string
	for _, ligne := range strings.Split(sortie, "\n") {
		if reste, porte := strings.CutPrefix(ligne, prefixe); porte {
			relevees = append(relevees, reste)
		}
	}
	if len(relevees) != attendues {
		t.Fatalf("%d ligne(s) « %s », attendu %d :\n%s", len(relevees), prefixe, attendues, sortie)
	}
	if !slices.IsSorted(relevees) {
		t.Errorf("les lignes « %s » ne sont pas triées : %q", prefixe, relevees)
	}
}

// Un identifiant inconnu sort en erreur, et le binaire avec un code non nul :
// une comparaison vide passerait pour deux passes identiques.
func TestComparerSurUnIdInconnuEchoue(t *testing.T) {
	app := baseNeuve(t)

	passe := passeConstruite(t, app, formeVoulue{
		brut:    "sel",
		lecture: lectureDUneForme{Aliment: "sel"},
		motif:   "aliment_nu",
		resolu:  true,
	})

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "comparer", passe.Id, "inconnu00000000")
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

// Deux passes sans forme commune ne rendent pas un taux calculé sur zéro : le
// quotient ne se fait pas, et la sortie le dit plutôt que d'écrire un chiffre
// inventé — ou, pire, « NaN ».
func TestComparerDeuxPassesSansFormeCommuneNeDivisePasParZero(t *testing.T) {
	app := baseNeuve(t)

	avant := passeConstruite(t, app, formeVoulue{
		brut:    "sel",
		lecture: lectureDUneForme{Aliment: "sel"},
		motif:   "aliment_nu",
		resolu:  true,
	})
	apres := passeConstruite(t, app, formeVoulue{
		brut:    "poivre",
		lecture: lectureDUneForme{Aliment: "poivre"},
		motif:   "aliment_nu",
		resolu:  true,
	})

	sortie := comparaisonDeDeuxPasses(t, app, avant, apres)

	exigeLesLignes(t, sortie,
		"formes communes : 0",
		"taux de résolution : avant —, après —, écart — points",
	)
	if strings.Contains(sortie, "NaN") {
		t.Errorf("la sortie porte un « NaN » :\n%s", sortie)
	}
}

// Le rapprochement se fait sur la ligne brute, espaces de bord retirés : c'est
// le même repli que celui de l'analyse, qui fait de « 1 pincée de sel » et de
// «  1 pincée de sel  » une seule et même forme. Sans lui, une ligne que
// l'exploitant a saisie avec une espace de trop serait comptée des deux côtés
// comme une forme que l'autre ne connaît pas.
//
// Et le dédoublonnage : la même ligne écrite dans deux recettes ne fait qu'une
// forme côté base, comme elle n'en fait qu'une côté passe.
func TestComparerLaBaseReplieLesEspacesEtDedoublonneSurRaw(t *testing.T) {
	a := analyseurDeTest(t)
	app := baseNeuveAvec(t, a)

	premiere := recetteNeuve(t, app)
	for _, brut := range []string{"3 pommes", "  1 pincée de sel  "} {
		ligne := ingredientNeuf(t, app, premiere, brut)
		if err := app.Save(ligne); err != nil {
			t.Fatalf("enregistrement de la ligne %q : %v", brut, err)
		}
	}
	// La même ligne dans une seconde recette : un doublon pour la base, une
	// seule forme pour la comparaison.
	seconde := recetteNeuve(t, app)
	doublon := ingredientNeuf(t, app, seconde, "3 pommes")
	if err := app.Save(doublon); err != nil {
		t.Fatalf("enregistrement du doublon : %v", err)
	}

	o := nouvelOuvrierDAnalyse(app, horlogeFigee(), a)
	passe, err := o.lance(context.Background(), sourceInstance, lignesDeLInstance(app))
	if err != nil {
		t.Fatalf("analyse refusée : %v", err)
	}

	sortie, aEchoue, err := executeLaCommande(t, app, "analyse", "comparer", "--base", passe.Id)
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	if aEchoue {
		t.Errorf("témoin d'échec levé sur une commande qui a réussi :\n%s", sortie)
	}

	exigeLesLignes(t, sortie,
		// Deux formes, pas trois : le doublon n'en fait pas une de plus. Et
		// aucune forme d'un seul côté : la ligne entourée d'espaces s'est
		// rapprochée de la forme que la passe en a tirée.
		"formes communes : 2",
		"formes seulement avant : 0",
		"formes seulement après : 0",
		"formes identiques : 2",
	)
}
