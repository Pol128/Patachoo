package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"
)

// Les cinq signaux, ligne par ligne. Une table plutôt qu'un test par signal :
// ce qui compte n'est pas qu'un signal s'allume, c'est que les quatre autres
// restent éteints sur la même ligne — et seule la tranche entière le dit.
//
// Les lignes viennent du corpus de la forge, pas d'exemples inventés : chacune
// a été passée au moteur épinglé avant d'être écrite ici.
func TestLesSignauxDUneLigne(t *testing.T) {
	a := analyseurDeTest(t)

	cas := []struct {
		brut     string
		attendus []string
	}{
		// Rien à signaler : lue entièrement, et résolue par le lexique.
		{"1 c. à s. rase de sucre", []string{}},
		{"2 à 3 gousses d'ail", []string{}},
		{"1/2 citron", []string{}},
		{"1 pincée de sel", []string{}},
		{"sel", []string{}},
		// La soudure chiffre/lettre : « 100g » est un mot pour l'œil et deux
		// pour le parser. Sans la coupe, « g » passerait pour perdu.
		{"100g de beurre", []string{}},
		// Le « (s) » que le moteur efface n'est porté par aucun champ. Sans la
		// normalisation du pack, il sortirait en mot perdu — et c'est la
		// régression qui rend le signal inutilisable.
		{"1 pincée(s) Sel", []string{}},
		// Le sac des champs se construit sur AlimentTexte et non sur Aliment :
		// « tomates » est écrit sur la ligne, « tomate » est la forme du
		// lexique. Prendre le canonique inventerait ici un mot perdu.
		{"2 tomates", []string{}},

		// Un simple trou de lexique n'allume que lui : c'est le cas qui dit
		// que les quatre autres signaux ne suivent pas.
		{"1 Oignon(s)", []string{"non_resolu"}},
		{"100 g de farine", []string{"non_resolu"}},
		{"2 tomates bien mûres (pelées)", []string{"non_resolu"}},
		{"200 g de crème fraîche épaisse, facultatif", []string{"non_resolu"}},
		// L'unité et l'aliment sont légitimement distincts : « unité répétée »
		// ne s'allume pas.
		{"1 gousse de vanille", []string{"non_resolu"}},
		{"3 branches de thym", []string{"non_resolu"}},

		// Le cas le plus fréquent du corpus, et la seule vraie perte du
		// parser : le second « de » n'est revendiqué par aucun champ.
		{"1/4 de litre de lait", []string{"mots_perdus"}},
		// Un mot rangé dans un booléen est perdu, et c'est voulu :
		// Approximative passe à vrai et « environ » reste derrière.
		{"environ 500 g de farine", []string{"mots_perdus", "non_resolu"}},

		// « 1 feuille de feuille de laurier » sur la fiche.
		{"1 feuille de laurier", []string{"unite_repetee"}},
		// Le pluriel de l'unité ne fait pas échapper à la règle : les deux
		// écritures portent la clé « feuille ».
		{"2 feuilles de laurier", []string{"unite_repetee"}},

		// Mêmes mots, ordre changé : la note est remontée en fin de ligne.
		// « mots perdus » reste éteint — les deux clés s'excluent.
		{"65 g de fruits secs au choix (sans sel)", []string{"mots_reordonnes", "non_resolu"}},

		// Un fragment de ligne coupé par la source : le moteur ne rend aucun
		// aliment.
		{"(+ 1/4 de la sauce de base)", []string{"aliment_vide", "non_resolu"}},
	}
	for _, c := range cas {
		t.Run(c.brut, func(t *testing.T) {
			obtenus := a.signaux(c.brut, a.lit(c.brut))
			if obtenus == nil {
				t.Fatal("signaux = nil, attendu une tranche — json_array_length veut un tableau des deux côtés")
			}
			if !slices.Equal(obtenus, c.attendus) {
				t.Errorf("signaux = %q, attendu %q", obtenus, c.attendus)
			}
		})
	}
}

// La coupe aux soudures chiffre/lettre, dans les deux sens, testée sur le sac
// de mots lui-même.
//
// Le sens chiffre → lettre se voit de bout en bout — « 100g » est une quantité
// et une unité, donc deux champs, et la table le dit. Le sens inverse, lui, ne
// se voit sur aucune ligne : « T55 » tient entier dans l'aliment, et un
// découpage qui le laisserait soudé donnerait le même sac des deux côtés. Sans
// ce test, la moitié de la règle ne serait pas couverte.
func TestLesMotsSeCoupentAuxSouduresDansLesDeuxSens(t *testing.T) {
	a := analyseurDeTest(t)

	cas := []struct {
		texte    string
		attendus []string
	}{
		{"100g", []string{"100", "g"}},
		{"farine T55", []string{"farine", "t", "55"}},
	}
	for _, c := range cas {
		t.Run(c.texte, func(t *testing.T) {
			if obtenus := a.mots(c.texte); !slices.Equal(obtenus, c.attendus) {
				t.Errorf("mots = %q, attendu %q", obtenus, c.attendus)
			}
		})
	}
}

// La tranche part en JSON dans une colonne et sera relue par des écrans : deux
// passes sur la même ligne doivent rendre le même tableau, octet pour octet.
// Un parcours de map suffirait à faire varier l'ordre d'une passe à l'autre.
func TestLesSignauxSontTriesEtStables(t *testing.T) {
	a := analyseurDeTest(t)

	// Une ligne qui allume plusieurs signaux : sur une seule clé, aucun ordre
	// ne se verrait.
	const brut = "environ 500 g de farine"

	premiere := a.signaux(brut, a.lit(brut))
	if !slices.IsSorted(premiere) {
		t.Errorf("signaux = %q, attendu trié", premiere)
	}
	for passe := range 20 {
		if suivante := a.signaux(brut, a.lit(brut)); !slices.Equal(suivante, premiere) {
			t.Fatalf("passe %d : signaux = %q, attendu %q", passe+2, suivante, premiere)
		}
	}
}

// resout rend l'entrée du lexique, et donc la catégorie : PATA-122 la range
// dans sa propre colonne, et l'ouvrier n'a pas à résoudre deux fois.
func TestResoutRendLEntreeEtSaCategorie(t *testing.T) {
	a := analyseurDeTest(t)

	cas := []struct {
		brut      string
		trouvee   bool
		categorie string
	}{
		{"1 feuille de laurier", true, "Herbes et épices"},
		{"2 à 3 gousses d'ail", true, "Légumes"},
		{"1/2 citron", true, "Fruits"},
		// Absent du lexique : rien à ranger dans les trois colonnes.
		{"100 g de farine", false, ""},
		{"(+ 1/4 de la sauce de base)", false, ""},
	}
	for _, c := range cas {
		t.Run(c.brut, func(t *testing.T) {
			entree, trouvee := a.resout(a.lit(c.brut))
			if trouvee != c.trouvee {
				t.Fatalf("résolu = %v, attendu %v", trouvee, c.trouvee)
			}
			if entree.Label != c.categorie {
				t.Errorf("catégorie = %q, attendu %q", entree.Label, c.categorie)
			}
		})
	}
}

// « Fonctions pures » se vérifie à la lecture des signatures et des imports —
// autant le faire lire par le compilateur des tests plutôt que par un humain.
// Le jour où l'ouvrier de PATA-123 branchera ces fonctions, la tentation sera
// de leur passer l'app plutôt que de porter le résultat.
func TestSignauxNOuvreNiBaseNiFichier(t *testing.T) {
	fichier, err := parser.ParseFile(token.NewFileSet(), "signaux.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("lecture de signaux.go : %v", err)
	}

	interdits := []string{"os", "time", "io/ioutil", "net/http", "github.com/pocketbase/"}
	for _, importe := range fichier.Imports {
		chemin := strings.Trim(importe.Path.Value, `"`)
		for _, interdit := range interdits {
			if chemin == interdit || strings.HasPrefix(chemin, interdit) {
				t.Errorf("signaux.go importe %q : ni base, ni horloge, ni fichier", chemin)
			}
		}
	}

	ast.Inspect(fichier, func(n ast.Node) bool {
		champs, estType := n.(*ast.SelectorExpr)
		if !estType {
			return true
		}
		paquet, estNom := champs.X.(*ast.Ident)
		if !estNom {
			return true
		}
		if paquet.Name == "core" || paquet.Name == "time" || paquet.Name == "context" || paquet.Name == "os" {
			t.Errorf("signaux.go mentionne %s.%s : ni base, ni horloge, ni fichier", paquet.Name, champs.Sel.Name)
		}
		return true
	})
}
