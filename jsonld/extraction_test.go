package jsonld

import (
	"errors"
	"strings"
	"testing"
)

// Ce fichier ne teste que ce que le corpus ne porte pas : les causes d'échec
// prises deux à deux, leur précédence quand une page mêle plusieurs blocs, le
// repérage des blocs dans le document, et la tenue de l'extracteur face à des
// entrées dégénérées. Les formes de balisage, elles, sont jugées par
// testdata/ — les redire ici ferait rougir deux tests là où un suffit.

// page enveloppe des blocs ld+json dans une page minimale.
func page(blocs ...string) []byte {
	var b strings.Builder
	b.WriteString("<!doctype html><html lang=\"fr\"><head><title>page de test</title>")
	for _, bloc := range blocs {
		b.WriteString(`<script type="application/ld+json">`)
		b.WriteString(bloc)
		b.WriteString("</script>")
	}
	b.WriteString("</head><body><h1>page de test</h1></body></html>")
	return []byte(b.String())
}

const recetteMinimale = `{"@context":"https://schema.org","@type":"Recipe","name":"Soupe témoin"}`

// PATA-9 distingue les causes par errors.Is, et compare le message à la
// constante. Les deux doivent tenir.
func TestLesQuatreCausesSeDistinguentDeuxADeux(t *testing.T) {
	causes := map[string]error{
		SansRecette:   ErrSansRecette,
		AucunBalisage: ErrAucunBalisage,
		JSONInvalide:  ErrJSONInvalide,
		TitreAbsent:   ErrTitreAbsent,
	}

	for nom, cause := range causes {
		if cause.Error() != nom {
			t.Errorf("message %q, attendu %q", cause.Error(), nom)
		}
		for autreNom, autre := range causes {
			estLaMeme := errors.Is(cause, autre)
			if nom == autreNom && !estLaMeme {
				t.Errorf("%s ne se reconnaît pas lui-même", nom)
			}
			if nom != autreNom && estLaMeme {
				t.Errorf("errors.Is confond %s et %s", nom, autreNom)
			}
		}
	}
}

func TestPrecedenceDesCausesDEchec(t *testing.T) {
	illisible := `{"@type":"Recipe","name":"Gâteau",}`

	cas := []struct {
		nom    string
		page   []byte
		erreur error
		titre  string
	}{
		{
			nom:   "un bloc illisible n'interrompt pas le parcours",
			page:  page(illisible, recetteMinimale),
			titre: "Soupe témoin",
		},
		{
			nom:    "un bloc illisible et aucune recette ailleurs",
			page:   page(illisible, `{"@type":"Article","headline":"Les soupes"}`),
			erreur: ErrJSONInvalide,
		},
		{
			nom:    "tous les blocs lisibles, aucune recette",
			page:   page(`{"@type":"Article"}`, `{"@type":"Organization","name":"Perlimpinpin"}`),
			erreur: ErrSansRecette,
		},
		{
			nom:    "la première recette fait foi, même sans nom",
			page:   page(`{"@type":"Recipe","recipeIngredient":["2 pommes"]}`, recetteMinimale),
			erreur: ErrTitreAbsent,
		},
		{
			nom:    "un nom vide ne vaut pas un nom",
			page:   page(`{"@type":"Recipe","name":"   "}`),
			erreur: ErrTitreAbsent,
		},
		{
			nom:    "pas un bloc sur la page",
			page:   []byte("<html><head></head><body><p>rien à déclarer</p></body></html>"),
			erreur: ErrAucunBalisage,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			recette, err := Extraire(c.page)

			if c.erreur != nil {
				if !errors.Is(err, c.erreur) {
					t.Fatalf("erreur %v, attendue %v", err, c.erreur)
				}
				if recette.Titre != "" {
					t.Errorf("un échec rend une recette : %q", recette.Titre)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if recette.Titre != c.titre {
				t.Errorf("titre %q, attendu %q", recette.Titre, c.titre)
			}
		})
	}
}

// Le repérage passe par un parseur HTML, pas par une expression régulière :
// c'est ce que ces cas vérifient, chacun étant un endroit où une regexp se
// tromperait.
func TestLeReperageDesBlocsSuitLeDocument(t *testing.T) {
	cas := []struct {
		nom    string
		page   string
		titre  string
		erreur error
	}{
		{
			nom:   "attributs dans un autre ordre, en guillemets simples",
			page:  `<html><head><script id="ld" type='application/ld+json' async>` + recetteMinimale + `</script></head><body></body></html>`,
			titre: "Soupe témoin",
		},
		{
			nom:   "type écrit avec des majuscules et des espaces",
			page:  `<html><head><script type=" Application/LD+JSON ">` + recetteMinimale + `</script></head><body></body></html>`,
			titre: "Soupe témoin",
		},
		{
			nom:   "bloc dans le corps et non dans l'entête",
			page:  `<html><head></head><body><h1>x</h1><script type="application/ld+json">` + recetteMinimale + `</script></body></html>`,
			titre: "Soupe témoin",
		},
		{
			nom:    "un bloc en commentaire n'est pas un bloc",
			page:   `<html><head><!-- <script type="application/ld+json">` + recetteMinimale + `</script> --></head><body></body></html>`,
			erreur: ErrAucunBalisage,
		},
		{
			nom:    "un script d'un autre type est ignoré",
			page:   `<html><head><script type="application/json">` + recetteMinimale + `</script></head><body></body></html>`,
			erreur: ErrAucunBalisage,
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			recette, err := Extraire([]byte(c.page))

			if c.erreur != nil {
				if !errors.Is(err, c.erreur) {
					t.Fatalf("erreur %v, attendue %v", err, c.erreur)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if recette.Titre != c.titre {
				t.Errorf("titre %q, attendu %q", recette.Titre, c.titre)
			}
		})
	}
}

// Une durée est un agrément, pas une condition : ce qu'on ne sait pas lire
// laisse le champ à zéro sans faire échouer l'extraction. Les durées qu'on sait
// lire sont jugées par testdata/durees-iso8601.
func TestUneDureeIllisibleLaisseLeChampAZeroSansFaireEchouer(t *testing.T) {
	illisibles := []string{
		`"prepTime":"trente minutes"`,
		`"prepTime":"PT"`,
		`"prepTime":"1H30M"`,
		`"prepTime":"PT45S"`,
		`"prepTime":"P1DT2H"`,
		`"prepTime":"PTxM"`,
		`"prepTime":25`,
		`"prepTime":null`,
	}

	for _, duree := range illisibles {
		t.Run(duree, func(t *testing.T) {
			recette, err := Extraire(page(`{"@type":"Recipe","name":"Soupe témoin",` + duree + `}`))
			if err != nil {
				t.Fatalf("erreur inattendue : %v", err)
			}
			if recette.PreparationMin != 0 {
				t.Errorf("préparation %d min, attendu 0", recette.PreparationMin)
			}
		})
	}
}

// Aucune entrée, si tordue soit-elle, ne doit paniquer : l'extracteur reçoit ce
// qu'un site tiers a bien voulu répondre.
func TestLesEntreesHostilesNePaniquentPas(t *testing.T) {
	profond := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	tresProfond := strings.Repeat("[", 40000) + strings.Repeat("]", 40000)

	entrees := map[string][]byte{
		"page vide":                              []byte(""),
		"html tronqué en plein bloc":             []byte(`<html><head><script type="application/ld+json">{"@type":"Recipe","name":"Sou`),
		"bloc vide":                              page(""),
		"bloc d'espaces":                         page("   \n  "),
		"json valant null":                       page("null"),
		"json valant un nombre":                  page("42"),
		"nom en nombre":                          page(`{"@type":"Recipe","name":42}`),
		"type en tableau de nombres":             page(`{"@type":[42,{"a":1}],"name":"Soupe témoin"}`),
		"ingrédients en objet":                   page(`{"@type":"Recipe","name":"Soupe témoin","recipeIngredient":{"a":"b"}}`),
		"instructions nulles":                    page(`{"@type":"Recipe","name":"Soupe témoin","recipeInstructions":null}`),
		"instructions en objets sans text":       page(`{"@type":"Recipe","name":"Soupe témoin","recipeInstructions":[{"@type":"HowToStep","name":"Étape"},null,42]}`),
		"image en tableau vide":                  page(`{"@type":"Recipe","name":"Soupe témoin","image":[]}`),
		"graphe valant une chaîne":               page(`{"@graph":"pas un tableau"}`),
		"imbrication de milliers de niveaux":     page(profond),
		"imbrication que le parseur JSON refuse": page(tresProfond),
	}

	nommees := []error{ErrSansRecette, ErrAucunBalisage, ErrJSONInvalide, ErrTitreAbsent}

	for nom, entree := range entrees {
		t.Run(nom, func(t *testing.T) {
			recette, err := Extraire(entree)

			if err == nil {
				if recette.Titre == "" {
					t.Error("extraction réussie sans titre")
				}
				return
			}
			for _, nommee := range nommees {
				if errors.Is(err, nommee) {
					return
				}
			}
			t.Errorf("erreur hors des quatre causes nommées : %v", err)
		})
	}
}
