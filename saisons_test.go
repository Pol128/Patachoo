package main

import (
	"slices"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// --- La table (PATA-18) -----------------------------------------------------

// La table de saisons vit dans le paquet main, et le schéma vit dans
// migrations, où la liste est privée au paquet. Rien ne relie donc les deux à
// la compilation : c'est ce test qui les relie, et lui seul.
//
// La comparaison porte sur l'ensemble et non sur l'ordre : le schéma range les
// saisons dans l'ordre de l'année, la table pourrait un jour se ranger
// autrement sans que rien ne soit cassé. Ce qui doit rougir, c'est une saison
// ajoutée d'un côté et pas de l'autre.
func TestLaTableDesSaisonsSuitLeSchema(t *testing.T) {
	app := baseNeuveAvec(t, analyseurDeTest(t))

	recettes, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	champ, ok := recettes.Fields.GetByName("seasons").(*core.SelectField)
	if !ok {
		t.Fatalf("le champ seasons n'est pas un select")
	}

	duSchema := slices.Clone(champ.Values)
	slices.Sort(duSchema)

	deLaTable := make([]string, 0, len(lesSaisons))
	for _, saison := range lesSaisons {
		deLaTable = append(deLaTable, saison.Valeur)
	}
	slices.Sort(deLaTable)

	if !slices.Equal(duSchema, deLaTable) {
		t.Errorf("le schéma porte %q, la table %q — une saison ajoutée d'un côté doit l'être de l'autre",
			champ.Values, deLaTable)
	}
}

// --- La saison du jour ------------------------------------------------------

// Découpage météorologique, par mois entiers : les bornes astronomiques
// tombent vers le 20 et glissent d'une année sur l'autre.
func TestLaSaisonSuitLeMois(t *testing.T) {
	attendues := map[time.Month]string{
		time.January: "hiver", time.February: "hiver", time.December: "hiver",
		time.March: "printemps", time.April: "printemps", time.May: "printemps",
		time.June: "été", time.July: "été", time.August: "été",
		time.September: "automne", time.October: "automne", time.November: "automne",
	}

	for mois := time.January; mois <= time.December; mois++ {
		if obtenue := saisonDuMois(mois); obtenue != attendues[mois] {
			t.Errorf("saisonDuMois(%v) = %q, attendu %q", mois, obtenue, attendues[mois])
		}
	}
}
