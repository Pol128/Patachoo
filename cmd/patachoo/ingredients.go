package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Pol128/moteur"
	"github.com/pocketbase/pocketbase/core"
)

// analyseur porte le pack de langue et le lexique d'aliments, chargés une
// seule fois, et le décompte des lignes que le moteur n'a pas su lire.
//
// Le couple est immuable après chargement : c'est ce qui permet de le
// partager entre toutes les écritures sans le relire à chaque ligne.
type analyseur struct {
	pack    *moteur.Pack
	lexique *moteur.Lexique

	// Le compteur est écrit depuis les hooks, qui peuvent tourner en
	// parallèle sur plusieurs requêtes : il se protège.
	mu     sync.Mutex
	compte int
	motifs map[string]int
}

// analyseurFR charge le français : pack et lexique sont embarqués dans le
// module moteur, il n'y a aucun fichier à porter côté Patachoo.
//
// À appeler une fois, au démarrage. Un échec ici porte sur des données
// compilées dans le binaire : ce n'est pas une erreur d'exploitation, c'est un
// binaire corrompu. Il arrête le démarrage, il ne se rattrape pas.
func analyseurFR() (*analyseur, error) {
	pack, lexique, err := moteur.FR()
	if err != nil {
		return nil, fmt.Errorf("pack de langue français : %w", err)
	}
	return &analyseur{pack: pack, lexique: lexique, motifs: map[string]int{}}, nil
}

// lit rend la lecture d'une ligne brute par le moteur.
//
// Le seul chemin par lequel une ligne atteint le moteur : le pack et le
// lexique ne s'attrapent pas ailleurs.
func (a *analyseur) lit(brut string) *moteur.Ingredient {
	return moteur.Lit(brut, a.pack, a.lexique)
}

// compteNonLue enregistre une ligne que le moteur n'a pas su lire et rend le
// total et le décompte du motif en cause.
func (a *analyseur) compteNonLue(motif string) (total, pourCeMotif int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.compte++
	a.motifs[motif]++
	return a.compte, a.motifs[motif]
}

// nonLues rend le nombre de lignes que le moteur n'a pas su lire depuis le
// démarrage. C'est la mesure qui dira si le pack de langue doit être complété.
func (a *analyseur) nonLues() int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.compte
}

// motifsNonLus rend la répartition des lignes non lues par motif : c'est elle
// qui dit quelle règle du pack manque, là où le total dit seulement qu'il en
// manque une.
func (a *analyseur) motifsNonLus() map[string]int {
	a.mu.Lock()
	defer a.mu.Unlock()

	copie := make(map[string]int, len(a.motifs))
	for motif, n := range a.motifs {
		copie[motif] = n
	}
	return copie
}

// champsIngredient porte les cinq champs qu'une lecture alimente. raw n'en
// fait pas partie : il est écrit tel quel et jamais recalculé.
type champsIngredient struct {
	quantite  *float64
	unite     string
	aliment   string
	note      string
	optionnel bool
}

// champsLus rend les cinq champs dérivés d'une lecture. Fonction pure : ni
// base, ni serveur, ni horloge — c'est la table de correspondance, et elle se
// relit d'un coup d'œil.
//
// unit prend Unite.Abrev, et pas UniteCle() ni UniteTexte : c'est le seul des
// trois qui soit à la fois canonique et lisible. UniteCle() rendrait
// « cuillere_a_soupe » sur une fiche ; UniteTexte garde la graisse d'origine,
// donc deux écritures de la même unité ne se regrouperaient jamais.
//
// Ce que le schéma ne porte pas est abandonné, et raw en garde la trace :
// Partitif, Qualificatifs et QuantiteTexte, mais aussi QuantiteMax,
// Approximative et Indefinie — quantity reçoit Quantite telle quelle. La
// perte est assumée ici, et suivie dans PATA-38.
func champsLus(lu *moteur.Ingredient) champsIngredient {
	champs := champsIngredient{
		quantite:  lu.Quantite,
		aliment:   lu.Aliment,
		note:      lu.Note,
		optionnel: lu.Optionnel,
	}
	if lu.Unite != nil {
		champs.unite = lu.Unite.Abrev
	}
	return champs
}

// pose écrit les cinq champs sur l'enregistrement.
//
// quantity vide devient 0 : le champ nombre de PocketBase n'est pas nullable.
// C'est sans ambiguïté à la lecture — un ingrédient dosé à zéro n'existe pas.
func (c champsIngredient) pose(enregistrement *core.Record) {
	if c.quantite == nil {
		enregistrement.Set("quantity", nil)
	} else {
		enregistrement.Set("quantity", *c.quantite)
	}
	enregistrement.Set("unit", c.unite)
	enregistrement.Set("food", c.aliment)
	enregistrement.Set("note", c.note)
	enregistrement.Set("optional", c.optionnel)
}

// brancheLIngredient accroche la lecture des lignes brutes à l'app.
//
// Un hook plutôt qu'un appel chez chaque appelant : il couvre d'un coup
// l'import, le formulaire, l'API REST et l'interface d'administration. Un
// chemin d'écriture ajouté plus tard est couvert sans qu'on ait à y penser,
// ce qui est exactement là où on oublierait.
func brancheLIngredient(app core.App, a *analyseur) {
	// À la création, les cinq champs sont remplis à partir de raw même s'ils
	// étaient fournis dans la requête : sinon un client choisirait ce que la
	// fiche affiche, sans rapport avec la ligne enregistrée.
	app.OnRecordCreate("ingredients").BindFunc(a.litLaLigne)
	app.OnRecordUpdate("ingredients").BindFunc(a.relitSiRawAChange)
}

// relitSiRawAChange ne recalcule que si la ligne brute a bougé.
//
// C'est ce qui fait survivre une correction manuelle de quantity ou de food à
// l'enregistrement suivant : le moteur a lu ce qu'il pouvait, l'utilisateur a
// corrigé le reste, et un recalcul systématique effacerait la correction.
func (a *analyseur) relitSiRawAChange(e *core.RecordEvent) error {
	if e.Record.GetString("raw") == e.Record.Original().GetString("raw") {
		return e.Next()
	}
	return a.litLaLigne(e)
}

// litLaLigne lit la ligne brute, en pose les champs dérivés, et journalise ce
// que le moteur n'a pas su lire.
func (a *analyseur) litLaLigne(e *core.RecordEvent) error {
	brut := e.Record.GetString("raw")
	if strings.TrimSpace(brut) == "" {
		// Le champ est déclaré obligatoire, mais la validation laisserait
		// passer une ligne d'espaces — qui n'est pas un ingrédient, et que
		// personne ne pourrait ensuite distinguer d'une erreur d'import.
		return errors.New("raw : une ligne d'ingrédient vide ne s'enregistre pas")
	}

	lu := a.lit(brut)
	champsLus(lu).pose(e.Record)

	// Est « non lue » une ligne dont Aliment ressort vide : c'est le résultat
	// vide de la règle de conduite. Un motif aliment_nu n'est pas un échec —
	// « sel » est une ligne parfaitement lue, sans quantité ni unité.
	if lu.Aliment == "" {
		total, pourCeMotif := a.compteNonLue(lu.Motif)
		e.App.Logger().Warn(
			"ligne d'ingrédient non lue",
			"raw", brut,
			"motif", lu.Motif,
			"non_lues", total,
			"non_lues_pour_ce_motif", pourCeMotif,
		)
	}

	return e.Next()
}

// accorde rend l'aliment tel qu'il s'affiche pour cette quantité : « tomate »
// seul, « tomates » à partir de deux.
//
// Ce qui est stocké est canonique, ce qui est affiché est accordé. La colonne
// food porte la forme du lexique — c'est elle qui fera se regrouper deux
// écritures du même aliment —, et la fiche lirait sinon « 2 oignon ».
//
// Le seuil vient du pack, comme pour les unités (Pack.Accorde) : l'anglais
// accorde dès un, le français à partir de deux, et une quantité fractionnaire
// reste donc au singulier. Une quantité absente vaut zéro dans le schéma —
// quantity n'est pas nullable — et passe par le même chemin.
//
// Un aliment que le lexique ne résout pas ressort tel quel, et un aliment dont
// l'entrée ne porte pas de pluriel aussi : la marque ne se déduit pas, « cœurs
// d'artichaut » la met sur le premier mot.
func (a *analyseur) accorde(aliment string, quantite float64) string {
	if quantite < a.pack.SeuilPluriel {
		return aliment
	}
	entree, trouvee := a.lexique.Resout(a.pack.Normalise(aliment))
	if !trouvee || entree.Pluriel == "" {
		return aliment
	}
	return entree.Pluriel
}
