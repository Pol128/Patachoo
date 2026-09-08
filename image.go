package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"net/url"
	"slices"
	"strings"

	// Les décodeurs des trois formats acceptés, enregistrés pour que
	// image.DecodeConfig sache lire leur en-tête. Le WebP vient de
	// golang.org/x/image, que PocketBase importe déjà pour ses miniatures :
	// s'appuyer sur son import à lui rendrait notre lecture dépendante d'un
	// détail de sa mise en œuvre.
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"

	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/Pol128/Patachoo/recuperation"
)

// L'image d'une recette importée est téléchargée par le serveur au moment où
// l'utilisateur valide le formulaire, puis rangée dans le champ fichier de la
// collection. Après ça, la fiche ne dépend plus du site d'origine : une source
// qui disparaît, qui réorganise ses URLs ou qui refuse les requêtes venues
// d'ailleurs ne fait plus disparaître l'illustration.
//
// L'URL traverse le formulaire dans un champ caché. Elle est donc postée par le
// client, et se traite exactement comme l'URL d'import : par le récupérateur de
// PATA-8, avec ses valeurs par défaut et sa politique d'adresses appliquée à
// l'adresse résolue. Un champ caché n'est pas un champ de confiance.
//
// filesystem.NewFileFromURL de PocketBase fait, en apparence, ce qu'il faut.
// Elle n'est pas employée ici : elle appelle http.DefaultClient sans plafond de
// taille, sans délai, sans contrôle d'adresse ni de schéma, redirections par
// défaut — ce serait le SSRF que PATA-8 ferme, rouvert par la porte de derrière.

// formatsAcceptes sont les trois formats qu'un téléchargement attache.
//
// Le schéma en accepte un quatrième, image/avif, et c'est voulu : un fichier que
// l'utilisateur téléverse lui-même est son choix. Un AVIF venu d'un site tiers,
// lui, n'est pas attaché — la bibliothèque dont PocketBase se sert pour
// fabriquer les miniatures ne le décode pas, et sert alors l'original. Une image
// en pleine résolution dans une grille de vignettes est précisément ce que la
// miniature existe pour éviter.
//
// Ce sont les noms que rend image.DecodeConfig, qui lit l'en-tête : ni
// l'extension de l'URL, ni le Content-Type annoncé par le site n'entrent dans la
// décision.
var formatsAcceptes = []string{"jpeg", "png", "webp"}

// pixelsMax borne les dimensions d'une image téléchargée : quarante mégapixels,
// soit environ 8000 × 5000, très au-dessus de toute photo de recette.
//
// Le plafond de cinq mébioctets du récupérateur borne les octets transférés, pas
// la mémoire nécessaire au décodage. Un PNG uni compresse énormément, et c'est
// PocketBase qui le décodera plus tard, à la première miniature demandée : le
// garde-fou se pose donc ici, où l'en-tête est lu, et pas là-bas.
//
// Une variable et non une constante, pour que les tests puissent l'abaisser :
// fabriquer une image de quarante mégapixels coûterait plus cher que ce qu'elle
// prouverait.
var pixelsMax int64 = 40_000_000

// nomDeLImageTelechargee est le nom passé au stockage, et il n'a pas
// d'extension : c'est ainsi que PocketBase la déduit du contenu.
//
// Son normalizeName ne renifle les octets que si le nom qu'on lui donne n'a pas
// d'extension exploitable. Un « .jpg » repris de l'URL rangerait donc un PNG
// sous une extension fausse — et aucun morceau d'une URL distante n'a à devenir
// un nom de fichier.
const nomDeLImageTelechargee = "image"

// optionsDuTelechargement s'ajoutent aux valeurs par défaut du récupérateur.
//
// Vide en production, et c'est tout l'intérêt : le téléchargement passe toujours
// par recuperation.Recupere, avec ses cinq mébioctets, ses dix secondes, ses
// cinq redirections et sa politique d'adresses. Les tests y posent la résolution
// de noms injectée, la levée de politique pour leurs serveurs de boucle locale
// et le transport piégé — jamais un faux récupérateur, qui passerait encore le
// jour où l'image cesserait de passer par celui-ci.
var optionsDuTelechargement []recuperation.Option

// adresseDeLImage rend l'URL absolue à aller chercher, ou la chaîne vide s'il
// n'y en a pas.
//
// Beaucoup de sites n'écrivent que le chemin de leurs images : une adresse
// relative se résout contre la source soumise avec le formulaire. Sans URL
// absolue à l'arrivée, rien ne part sur le réseau — la résolution précède le
// récupérateur, et donc le réseau.
func adresseDeLImage(brute, source string) string {
	brute = strings.TrimSpace(brute)
	if brute == "" {
		return ""
	}

	cible, err := url.Parse(brute)
	if err != nil {
		return ""
	}
	if cible.IsAbs() {
		return cible.String()
	}

	base, err := url.Parse(strings.TrimSpace(source))
	if err != nil || !base.IsAbs() {
		return ""
	}
	return base.ResolveReference(cible).String()
}

// URLDeLImage rend l'adresse à poster dans le champ caché du formulaire, ou la
// chaîne vide.
//
// Ce n'est pas toujours celle qui s'affiche. Le gabarit montre l'image distante
// dans un attribut src, où html/template neutralise un schéma exécutable ; un
// attribut value, lui, ne bénéficie d'aucune neutralisation, et une adresse en
// javascript: y atterrirait telle quelle. Le serveur n'irait de toute façon
// jamais la chercher : autant ne pas la reposter.
//
// Une adresse relative reste postée telle quelle — c'est la source soumise avec
// le formulaire qui la résoudra.
func (f formulaireRecette) URLDeLImage() string {
	adresse := strings.TrimSpace(f.ImageDistante)
	if adresse == "" {
		return ""
	}

	cible, err := url.Parse(adresse)
	if err != nil {
		return ""
	}
	if cible.IsAbs() && cible.Scheme != "http" && cible.Scheme != "https" {
		return ""
	}
	return adresse
}

// imageDistante va chercher l'image et rend le fichier à attacher.
//
// Le récupérateur porte la politique de sécurité — schéma, robots.txt,
// redirections comptées, plafond de taille, délai, et refus des adresses non
// routables sur l'adresse résolue. Rien n'en est réécrit ici : cette fonction ne
// juge que ce qui revient.
func imageDistante(ctx context.Context, adresse string) (*filesystem.File, error) {
	page, err := recuperation.Recupere(ctx, adresse, optionsDuTelechargement...)
	if err != nil {
		return nil, err
	}
	return imageAttachable(page.Corps)
}

// imageAttachable juge les octets rendus : le format et les dimensions se lisent
// tous deux dans l'en-tête de l'image, en une seule passe.
func imageAttachable(corps []byte) (*filesystem.File, error) {
	entete, format, err := image.DecodeConfig(bytes.NewReader(corps))
	if err != nil {
		return nil, fmt.Errorf("en-tête d'image illisible : %w", err)
	}
	if !slices.Contains(formatsAcceptes, format) {
		return nil, fmt.Errorf("format %q hors des trois acceptés", format)
	}
	if pixels := int64(entete.Width) * int64(entete.Height); pixels > pixelsMax {
		return nil, fmt.Errorf("image de %d pixels, au-delà du garde-fou de %d", pixels, pixelsMax)
	}
	return filesystem.NewFileFromBytes(corps, nomDeLImageTelechargee)
}
