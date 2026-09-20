package main

import (
	"slices"
	"strings"
	"unicode"

	"github.com/Pol128/moteur"
)

// Les cinq signaux de relecture. Sans référence extérieure, rien ne dit qu'une
// ligne est mal lue ; ces cinq-là se calculent à partir de la seule lecture, et
// ce sont eux qui trient ce qu'un humain regarde.
//
// Les valeurs sont écrites en base et relues par les écrans : elles ne se
// renomment plus sans migration de données. En français, comme les select du
// schéma.
const (
	SignalMotsPerdus     = "mots_perdus"
	SignalAlimentVide    = "aliment_vide"
	SignalUniteRepetee   = "unite_repetee"
	SignalNonResolu      = "non_resolu"
	SignalMotsReordonnes = "mots_reordonnes"
)

// signaux rend, triées et sans doublon, les clés des signaux qu'une ligne
// allume.
//
// Fonction pure : ni base, ni serveur, ni horloge, ni fichier, et aucun état
// muté — le compteur de lignes non lues et son verrou restent dans
// ingredients.go. Le pack et le lexique sont lus, jamais écrits, ce qui fait de
// ceci une méthode de l'analyseur plutôt qu'une fonction libre : deux des cinq
// signaux les interrogent.
//
// Une ligne bien lue rend une tranche vide, jamais nil : le résultat part en
// JSON dans une colonne, et json_array_length veut un tableau des deux côtés.
func (a *analyseur) signaux(brut string, lu *moteur.Ingredient) []string {
	allumes := make([]string, 0, 5)

	if lu.Aliment == "" {
		allumes = append(allumes, SignalAlimentVide)
	}
	if _, resolu := a.resout(lu); !resolu {
		allumes = append(allumes, SignalNonResolu)
	}
	if a.uniteRepetee(lu) {
		allumes = append(allumes, SignalUniteRepetee)
	}

	motsDuBrut := a.mots(brut)
	motsDesChamps := a.mots(champsRevendiquants(lu))
	switch {
	case manque(motsDuBrut, motsDesChamps):
		allumes = append(allumes, SignalMotsPerdus)
	case len(motsDuBrut) == len(motsDesChamps) && !slices.Equal(motsDuBrut, motsDesChamps):
		// Mêmes mots, ordre différent. On ne cherche l'ordre que lorsque rien
		// n'est perdu : c'est ce qui fait des deux clés des signaux distincts
		// plutôt qu'un doublon, et le faible ne recouvre jamais le fort.
		allumes = append(allumes, SignalMotsReordonnes)
	}

	slices.Sort(allumes)
	return allumes
}

// resout dit vers quelle entrée du lexique l'aliment canonique se résout.
//
// Rendu à part des signaux parce que PATA-122 range food, resolved et category
// dans trois colonnes : l'ouvrier lit l'entrée une fois pour ses colonnes, et
// signaux la relit pour la seule clé « non résolu ». C'est le même chemin que
// celui d'accorde, extrait pour être appelé une fois et lu deux.
func (a *analyseur) resout(lu *moteur.Ingredient) (moteur.Entree, bool) {
	return a.lexique.Resout(a.pack.Normalise(lu.Aliment))
}

// uniteRepetee dit si l'unité se retrouve en tête de l'aliment : « 1 feuille de
// laurier » donne unit=feuille et food=feuille de laurier, et la fiche affiche
// « 1 feuille de feuille de laurier ».
//
// La comparaison passe par la clé de l'unité et non par son texte : « 2
// feuilles de laurier » met « feuilles » face à un aliment au singulier, et les
// deux écritures portent la clé feuille.
//
// Le premier mot de l'aliment canonique, celui qui s'affiche et qui part dans
// la colonne food — que la ligne ait déjà répété ou non ne change rien, « 2
// gousse(s) Gousse(s) d'ail » est faux dans les deux cas.
func (a *analyseur) uniteRepetee(lu *moteur.Ingredient) bool {
	if lu.Unite == nil {
		return false
	}
	premier, _, _ := strings.Cut(strings.TrimSpace(lu.Aliment), " ")
	if premier == "" {
		return false
	}
	unite := a.pack.LitUniteExacte(premier)
	return unite != nil && unite.Cle == lu.Unite.Cle
}

// champsRevendiquants rend, dans l'ordre de la ligne, le texte que la lecture
// revendique.
//
// AlimentTexte, et surtout pas Aliment : le second porte la forme canonique du
// lexique, qui n'est pas forcément écrite sur la ligne — « 1 feuille de
// laurier » donne AlimentTexte = "laurier" et Aliment = "feuille de laurier".
// Prendre le canonique masquerait les vraies pertes et en inventerait d'autres.
//
// L'ordre compte : c'est lui que « mots réordonnés » compare à celui du brut.
func champsRevendiquants(lu *moteur.Ingredient) string {
	return strings.Join([]string{
		lu.QuantiteTexte,
		lu.UniteTexte,
		lu.Partitif,
		lu.AlimentTexte,
		lu.Note,
	}, " ")
}

// mots rend la suite des mots d'un texte, dans l'ordre.
//
// Le passage par Pack.Normalise n'est pas une commodité, c'est le signal
// lui-même : il efface la casse, les ligatures, les apostrophes, les accents et
// les marques de pluriel. Le moteur supprime le « (s) » de « 1 Oignon(s) », et
// plus aucun champ ne le porte — sans cette étape, chaque « (s) » du corpus
// sortirait en mot perdu et le signal ne servirait plus à rien.
func (a *analyseur) mots(texte string) []string {
	return decoupeEnMots(a.pack.Normalise(texte))
}

// decoupeEnMots rend les suites de lettres et de chiffres, tout le reste
// séparant, avec la coupe aux soudures chiffre/lettre dans les deux sens.
//
// « 100g » est un mot pour l'œil et deux pour le parser — une quantité et une
// unité. Sans la coupe, chaque soudure du corpus passerait pour une perte.
// C'est le _SOUDURE du crawler de la forge, dont ce découpage reprend la règle.
func decoupeEnMots(texte string) []string {
	var mots []string
	var courant strings.Builder
	var precedent rune

	termine := func() {
		if courant.Len() > 0 {
			mots = append(mots, courant.String())
			courant.Reset()
		}
	}
	for _, r := range texte {
		switch {
		case unicode.IsLetter(r):
			if unicode.IsDigit(precedent) {
				termine()
			}
			courant.WriteRune(r)
		case unicode.IsDigit(r):
			if unicode.IsLetter(precedent) {
				termine()
			}
			courant.WriteRune(r)
		default:
			termine()
		}
		precedent = r
	}
	termine()

	return mots
}

// manque dit si le premier sac de mots contient quelque chose que le second ne
// revendique pas, multiplicité comprise : « 1/4 de litre de lait » écrit « de »
// deux fois et la lecture n'en range qu'un.
func manque(cherches, revendiques []string) bool {
	reste := make(map[string]int, len(revendiques))
	for _, mot := range revendiques {
		reste[mot]++
	}
	for _, mot := range cherches {
		if reste[mot] == 0 {
			return true
		}
		reste[mot]--
	}
	return false
}
