package jsonld

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Le corpus est écrit de toutes pièces : contenu inventé, structures réelles.
// Voir testdata/LISEZMOI.md pour le pourquoi et la manière d'ajouter un cas.
//
// Tant que l'extraction n'existe pas (PATA-7), ces tests vérifient le corpus
// lui-même. C'est loin d'être inutile : un jeu de test faux se retourne contre
// celui qui s'y fie, et il se découvre d'habitude le jour où l'on débogue le
// code au lieu du test.

// Attendu est le résultat qu'on exige d'une page du corpus. Soit une recette,
// soit un échec nommé — jamais les deux, jamais aucun.
type Attendu struct {
	Cas     string   `json:"cas"`
	Erreur  string   `json:"erreur"`
	Recette *Recette `json:"recette"`
}

// casObligatoires : la liste que PATA-7 énumère. Elle est écrite ici pour
// qu'en retirer un fasse rougir un test, plutôt que de rétrécir le corpus en
// silence.
var casObligatoires = []string{
	"graphe-imbrique",
	"type-tableau",
	"instructions-chaine",
	"instructions-tableau-chaines",
	"instructions-howtostep",
	"instructions-howtosection",
	"image-objet",
	"image-tableau",
	"rendement-texte",
	"rendement-tableau",
	"durees-iso8601",
	"blocs-multiples",
	"entites-html",
	"ldjson-sans-recette",
	"aucun-balisage",
	"json-malforme",
	"titre-absent",
}

// casVolontairementIllisible : le seul dont le bloc ld+json ne doit pas être du
// JSON valide.
const casVolontairementIllisible = "json-malforme"

var blocLD = regexp.MustCompile(`(?is)<script[^>]+type="application/ld\+json"[^>]*>(.*?)</script>`)

func chargeCas(t *testing.T, nom string) (string, Attendu) {
	t.Helper()

	html, err := os.ReadFile(filepath.Join("testdata", nom+".html"))
	if err != nil {
		t.Fatalf("%s : %v", nom, err)
	}
	brut, err := os.ReadFile(filepath.Join("testdata", nom+".attendu.json"))
	if err != nil {
		t.Fatalf("%s sans résultat attendu : %v", nom, err)
	}
	var attendu Attendu
	if err := json.Unmarshal(brut, &attendu); err != nil {
		t.Fatalf("%s.attendu.json illisible : %v", nom, err)
	}
	return string(html), attendu
}

func TestTousLesCasObligatoiresSontLa(t *testing.T) {
	for _, nom := range casObligatoires {
		if _, err := os.Stat(filepath.Join("testdata", nom+".html")); err != nil {
			t.Errorf("cas %q manquant : %v", nom, err)
		}
	}
}

func TestChaquePageADesAttentesCoherentes(t *testing.T) {
	pages, err := filepath.Glob(filepath.Join("testdata", "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("corpus introuvable : %v", err)
	}

	echecsConnus := map[string]bool{
		SansRecette: true, AucunBalisage: true, JSONInvalide: true, TitreAbsent: true,
	}

	for _, page := range pages {
		nom := strings.TrimSuffix(filepath.Base(page), ".html")
		t.Run(nom, func(t *testing.T) {
			_, attendu := chargeCas(t, nom)

			if attendu.Cas == "" {
				t.Error("sans description : un cas dont on ne sait plus ce qu'il prouve finit par être supprimé")
			}

			switch {
			case attendu.Erreur != "":
				if !echecsConnus[attendu.Erreur] {
					t.Errorf("échec %q inconnu : il doit être l'une des constantes du paquet", attendu.Erreur)
				}
				if attendu.Recette != nil {
					t.Error("un échec attendu ne peut pas rendre une recette")
				}
			case attendu.Recette == nil:
				t.Error("ni recette ni échec attendus : ce cas n'exige rien")
			case attendu.Recette.Titre == "":
				t.Error("recette attendue sans titre")
			}
		})
	}
}

// Le corpus doit rester lisible par une machine — sauf le cas dont c'est
// justement le sujet.
func TestLesBlocsSontDuJSONValideSaufLeCasPrevuPourCa(t *testing.T) {
	pages, _ := filepath.Glob(filepath.Join("testdata", "*.html"))

	for _, page := range pages {
		nom := strings.TrimSuffix(filepath.Base(page), ".html")
		html, _ := chargeCas(t, nom)
		blocs := blocLD.FindAllStringSubmatch(html, -1)

		if nom == "aucun-balisage" {
			if len(blocs) != 0 {
				t.Errorf("%s : %d bloc(s) ld+json, alors que le cas est de n'en avoir aucun", nom, len(blocs))
			}
			continue
		}
		if len(blocs) == 0 {
			t.Errorf("%s : aucun bloc ld+json", nom)
			continue
		}

		valides := 0
		for _, bloc := range blocs {
			var quelconque any
			if json.Unmarshal([]byte(bloc[1]), &quelconque) == nil {
				valides++
			}
		}
		if nom == casVolontairementIllisible {
			if valides != 0 {
				t.Errorf("%s : le bloc est lisible, il ne prouve plus rien", nom)
			}
			continue
		}
		if valides != len(blocs) {
			t.Errorf("%s : %d bloc(s) sur %d illisibles", nom, len(blocs)-valides, len(blocs))
		}
	}
}

// Un .attendu.json sans page à côté est un reste d'un cas supprimé : il ne
// teste rien et laisse croire à une couverture qu'on n'a pas.
func TestPasDAttenteOrpheline(t *testing.T) {
	attentes, _ := filepath.Glob(filepath.Join("testdata", "*.attendu.json"))

	for _, attente := range attentes {
		nom := strings.TrimSuffix(filepath.Base(attente), ".attendu.json")
		if _, err := os.Stat(filepath.Join("testdata", nom+".html")); err != nil {
			t.Errorf("%s.attendu.json sans page correspondante", nom)
		}
	}
}
