package main

import "time"

// --- Les saisons ------------------------------------------------------------

// saison relie ce qui s'écrit dans une URL à ce que le schéma stocke.
//
// Deux orthographes, parce qu'elles n'ont pas le même métier : l'URL se tape,
// se recopie et se met en favori, donc elle est sans accent ; la base porte le
// nom tel qu'il s'affiche.
type saison struct {
	URL    string
	Valeur string
}

// lesSaisons est la table, fixe et fermée.
//
// Écrite ici plutôt qu'empruntée à migrations — où la liste est privée au
// paquet — ou dérivée par texte.Slug : l'ensemble ne bougera pas, personne
// n'ajoutera une cinquième saison, et c'est l'argument même qui a fait choisir
// un select plutôt qu'une collection. Un test relie la table au schéma, faute
// de quoi les deux pourraient diverger sans que rien ne le dise.
var lesSaisons = []saison{
	{URL: "printemps", Valeur: "printemps"},
	{URL: "ete", Valeur: "été"},
	{URL: "automne", Valeur: "automne"},
	{URL: "hiver", Valeur: "hiver"},
}

// horloge est la couture par où la date entre.
//
// Une variable de paquet plutôt qu'un paramètre traîné jusqu'à la route : « de
// saison » ne se testerait sinon qu'au mois où le test tourne. La zone est
// celle du serveur, et ça suffit — personne ne cuisine à cheval sur deux
// fuseaux.
var horloge = time.Now

// saisonDuMois rend la saison stockée d'un mois, par découpage météorologique.
//
// Météorologique et non astronomique : les bornes astronomiques tombent vers
// le 20 et glissent d'une année sur l'autre. Pour un carnet de recettes le
// mois est la bonne maille, et il se teste sans éphéméride.
func saisonDuMois(mois time.Month) string {
	switch mois {
	case time.March, time.April, time.May:
		return "printemps"
	case time.June, time.July, time.August:
		return "été"
	case time.September, time.October, time.November:
		return "automne"
	default:
		return "hiver"
	}
}

// saisonDuJour rend la saison stockée d'aujourd'hui : c'est ce que
// « ?saison=maintenant » désigne.
func saisonDuJour() string {
	return saisonDuMois(horloge().Month())
}

// valeurDeLaSaison traduit une valeur d'URL en valeur stockée, ou rend "".
//
// « maintenant » y est acceptée comme les quatre saisons : c'est elle que
// porte le lien « De saison », de façon qu'une URL mise en favori en décembre
// dise encore « de saison » en juin.
//
// Une chaîne vide pour tout le reste : l'ensemble est fermé et connu à la
// compilation, donc une valeur hors table ne peut être qu'une URL tapée de
// travers. Elle est ignorée, et la liste retombe sur le carnet entier.
func valeurDeLaSaison(url string) string {
	if url == "maintenant" {
		return saisonDuJour()
	}
	for _, saison := range lesSaisons {
		if saison.URL == url {
			return saison.Valeur
		}
	}
	return ""
}

// urlDeLaSaison fait le chemin inverse, pour les liens de la fiche.
//
// Rend "" pour une valeur que la table ne connaît pas. Le schéma restreint le
// champ aux quatre valeurs de la table, et un test interdit que les deux
// divergent : ce repli ne se rencontre donc pas, il évite seulement de
// fabriquer un lien vers une saison qui n'existe pas.
func urlDeLaSaison(valeur string) string {
	for _, saison := range lesSaisons {
		if saison.Valeur == valeur {
			return saison.URL
		}
	}
	return ""
}
