package jsonld

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"strconv"
	"strings"

	balisage "golang.org/x/net/html"
)

// Les quatre échecs, en erreurs. Elles sont construites sur les constantes du
// paquet : err.Error() vaut donc exactement sans_recette, aucun_balisage,
// json_invalide ou titre_absent, et l'appelant distingue les causes par
// errors.Is plutôt qu'en lisant un message.
var (
	ErrSansRecette   = errors.New(SansRecette)
	ErrAucunBalisage = errors.New(AucunBalisage)
	ErrJSONInvalide  = errors.New(JSONInvalide)
	ErrTitreAbsent   = errors.New(TitreAbsent)
)

// typeLD est le type des blocs qui portent du JSON-LD.
const typeLD = "application/ld+json"

// profondeurMax borne la descente dans une valeur JSON. Une recette vit à deux
// ou trois niveaux ; au-delà, c'est une page qui cherche à faire déborder la
// pile, et on cesse de chercher plutôt que de la suivre.
const profondeurMax = 32

// Extraire lit une page déjà récupérée et en tire la recette que son JSON-LD
// publie. Elle ne fait aucune requête et ne lit aucun fichier : aller chercher
// la page est l'affaire de PATA-8.
//
// L'échec est l'une des quatre causes nommées, jamais une autre.
func Extraire(page []byte) (Recette, error) {
	blocs := blocsLD(page)
	if len(blocs) == 0 {
		return Recette{}, ErrAucunBalisage
	}

	illisible := false
	for _, bloc := range blocs {
		var contenu any
		if err := json.Unmarshal([]byte(bloc), &contenu); err != nil {
			// Un bloc illisible n'interrompt pas le parcours : les sites en
			// publient plusieurs, et le suivant porte souvent la recette.
			illisible = true
			continue
		}
		if entite, trouvee := chercheRecette(contenu, 0); trouvee {
			// La première entité Recipe fait foi ; on ne cherche pas la
			// meilleure.
			return lisRecette(entite)
		}
	}

	if illisible {
		return Recette{}, ErrJSONInvalide
	}
	return Recette{}, ErrSansRecette
}

// blocsLD rend le contenu des blocs ld+json, dans l'ordre du document, entête
// comme corps de page.
//
// Le repérage passe par un vrai parseur, pas par une expression régulière : les
// pages réelles portent leurs attributs dans n'importe quel ordre, en
// guillemets simples, et commentent parfois un bloc entier — autant d'endroits
// où une regexp se trompe.
func blocsLD(page []byte) []string {
	racine, err := balisage.Parse(bytes.NewReader(page))
	if err != nil {
		// Le parseur se rattrape de tout HTML, et lire un tableau d'octets ne
		// faillit pas : il ne reste que l'imprévu, qui n'est pas du balisage.
		return nil
	}

	var blocs []string
	// Parcours itératif : une page à l'imbrication démesurée ne doit pas faire
	// déborder la pile.
	aVoir := []*balisage.Node{racine}
	for len(aVoir) > 0 {
		noeud := aVoir[len(aVoir)-1]
		aVoir = aVoir[:len(aVoir)-1]

		if noeud.Type == balisage.ElementNode && noeud.Data == "script" && estLD(noeud) {
			blocs = append(blocs, contenuTexte(noeud))
			continue
		}
		// Empilés à l'envers pour être dépilés dans l'ordre du document.
		for enfant := noeud.LastChild; enfant != nil; enfant = enfant.PrevSibling {
			aVoir = append(aVoir, enfant)
		}
	}
	return blocs
}

func estLD(noeud *balisage.Node) bool {
	for _, attribut := range noeud.Attr {
		if attribut.Key == "type" && strings.EqualFold(strings.TrimSpace(attribut.Val), typeLD) {
			return true
		}
	}
	return false
}

// contenuTexte rend le texte d'un élément. Le contenu d'un script est du texte
// brut : le parseur n'y décode aucune entité, et le JSON arrive intact.
func contenuTexte(noeud *balisage.Node) string {
	var texte strings.Builder
	for enfant := noeud.FirstChild; enfant != nil; enfant = enfant.NextSibling {
		if enfant.Type == balisage.TextNode {
			texte.WriteString(enfant.Data)
		}
	}
	return texte.String()
}

// chercheRecette trouve la première entité Recipe : à la racine, dans un
// @graph, ou dans un tableau d'entités.
func chercheRecette(valeur any, profondeur int) (map[string]any, bool) {
	if profondeur > profondeurMax {
		return nil, false
	}

	switch v := valeur.(type) {
	case map[string]any:
		if porteLeType(v["@type"], "Recipe") {
			return v, true
		}
		return chercheRecette(v["@graph"], profondeur+1)
	case []any:
		for _, element := range v {
			if entite, trouvee := chercheRecette(element, profondeur+1); trouvee {
				return entite, true
			}
		}
	}
	return nil, false
}

// porteLeType lit @type indifféremment en chaîne ou en tableau : une entité
// peut être à la fois une recette et un article.
func porteLeType(valeur any, attendu string) bool {
	switch v := valeur.(type) {
	case string:
		return v == attendu
	case []any:
		for _, element := range v {
			if nom, ok := element.(string); ok && nom == attendu {
				return true
			}
		}
	}
	return false
}

// lisRecette retient les champs qu'on sait montrer. Les autres sont ignorés,
// faute de place où les mettre.
func lisRecette(entite map[string]any) (Recette, error) {
	titre := texte(entite["name"])
	if titre == "" {
		// Sans titre, il n'y a pas de fiche à montrer : mieux vaut échouer que
		// d'en inventer un.
		return Recette{}, ErrTitreAbsent
	}

	return Recette{
		Titre:          titre,
		Description:    texte(entite["description"]),
		Image:          image(entite["image"]),
		Portions:       premiereEntree(entite["recipeYield"]),
		PreparationMin: minutes(texte(entite["prepTime"])),
		CuissonMin:     minutes(texte(entite["cookTime"])),
		Ingredients:    lignes(entite["recipeIngredient"]),
		Etapes:         etapes(entite["recipeInstructions"], 0),
	}, nil
}

// texte rend une valeur textuelle, entités HTML décodées — les sites en
// publient dans leur JSON-LD, et l'utilisateur n'a pas à les lire brutes. Le
// décodage vient après l'analyse du JSON, jamais avant : sur la page entière il
// casserait le JSON.
//
// Toute valeur qui n'est pas une chaîne rend une chaîne vide : un nombre là où
// le vocabulaire attend du texte n'est pas quelque chose qu'on sait montrer.
func texte(valeur any) string {
	chaine, estChaine := valeur.(string)
	if !estChaine {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(chaine))
}

// image lit une URL, que le site publie une chaîne, un ImageObject dont c'est
// url qui porte l'adresse, ou un tableau mêlant les deux — dont on retient la
// première entrée, quelle que soit sa forme.
func image(valeur any) string {
	valeur = premiere(valeur)
	if objet, estObjet := valeur.(map[string]any); estObjet {
		return texte(objet["url"])
	}
	return texte(valeur)
}

// premiereEntree conserve la mention telle quelle — « 4 » reste « 4 »,
// « 4 personnes » reste « 4 personnes ». La réduction à un nombre est
// l'affaire de PATA-9.
func premiereEntree(valeur any) string {
	return texte(premiere(valeur))
}

// premiere rend la première entrée d'un tableau, ou la valeur elle-même.
func premiere(valeur any) any {
	tableau, estTableau := valeur.([]any)
	if !estTableau {
		return valeur
	}
	if len(tableau) == 0 {
		return nil
	}
	return tableau[0]
}

// lignes lit une liste de chaînes en conservant l'ordre.
func lignes(valeur any) []string {
	tableau, estTableau := valeur.([]any)
	if !estTableau {
		return nil
	}

	var lues []string
	for _, element := range tableau {
		if ligne := texte(element); ligne != "" {
			lues = append(lues, ligne)
		}
	}
	return lues
}

// etapes lit recipeInstructions sous ses quatre formes — chaîne unique, tableau
// de chaînes, tableau de HowToStep, HowToSection contenant des HowToStep —, en
// conservant l'ordre.
func etapes(valeur any, profondeur int) []string {
	if profondeur > profondeurMax {
		return nil
	}

	switch v := valeur.(type) {
	case string:
		// Les sauts de ligne séparent les étapes, les lignes vides sont
		// ignorées.
		var lues []string
		for _, ligne := range strings.Split(v, "\n") {
			if etape := texte(ligne); etape != "" {
				lues = append(lues, etape)
			}
		}
		return lues
	case []any:
		var lues []string
		for _, element := range v {
			lues = append(lues, etapes(element, profondeur+1)...)
		}
		return lues
	case map[string]any:
		if porteLeType(v["@type"], "HowToSection") {
			// Les sections sont aplaties ; leur nom n'est pas une étape.
			return etapes(v["itemListElement"], profondeur+1)
		}
		// Un HowToStep porte l'étape dans text, pas dans name.
		if etape := texte(v["text"]); etape != "" {
			return []string{etape}
		}
	}
	return nil
}

// minutes convertit une durée ISO 8601 en minutes : PT25M vaut 25, PT3H30M vaut
// 210. Seules les heures et les minutes sont lues — c'est ce que les sites
// publient pour une recette. Une durée absente, vide ou d'une autre forme rend
// 0 : elle ne fait pas échouer l'extraction, elle manque, voilà tout.
func minutes(duree string) int {
	reste, commenceParPT := strings.CutPrefix(duree, "PT")
	if !commenceParPT || reste == "" {
		return 0
	}

	total, nombre := 0, ""
	for _, caractere := range reste {
		switch {
		case caractere >= '0' && caractere <= '9':
			nombre += string(caractere)
		case caractere == 'H', caractere == 'M':
			valeur, err := strconv.Atoi(nombre)
			if err != nil {
				return 0
			}
			if caractere == 'H' {
				valeur *= 60
			}
			total += valeur
			nombre = ""
		default:
			return 0
		}
	}
	if nombre != "" {
		// Un nombre sans son unité : on ne sait pas ce qu'il compte.
		return 0
	}
	return total
}
