package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// PATA-128. Le premier privilège du produit : l'établi est un écran réservé, et
// `exigeUneSession()` ne sait dire que « y a-t-il quelqu'un ? ».
//
// Un booléen, et non un champ à valeurs ouvertes : il se lit d'un coup d'œil
// dans /_/, et il n'invite pas à inventer des rôles qu'on ne saurait plus
// énumérer. Sans préfixe `is_`, comme `verified` et `open_registration`.
func TestLeChampCurateurEstUnBooleenSurUsers(t *testing.T) {
	app := baseNeuve(t)

	utilisateurs, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}

	champ := utilisateurs.Fields.GetByName("curator")
	if champ == nil {
		t.Fatal("users ne porte pas de champ curator : l'établi n'a aucun droit à exiger")
	}
	if _, ok := champ.(*core.BoolField); !ok {
		t.Errorf("curator est un %T, attendu un *core.BoolField", champ)
	}
}

// Le défaut d'un droit se choisit du côté qui refuse. Le compte est créé avant
// la migration, comme ceux d'une base déjà installée : c'est le seul montage où
// la question se pose vraiment — sur une base neuve, il n'y a personne à qui le
// champ pourrait arriver déjà coché.
func TestUnCompteAnterieurNArrivePasCurateur(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, "1789920900_droit_curateur.go")
	compte := compteNeuf(t, app, "ancien@exemple.test")

	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("remontée des migrations : %v", err)
	}

	relu, err := app.FindRecordById("users", compte.Id)
	if err != nil {
		t.Fatalf("relecture du compte : %v", err)
	}
	if relu.GetBool("curator") {
		t.Error("un compte antérieur à la migration arrive curateur : " +
			"la migration distribuerait le droit à toute une base installée")
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownRetireLeChampCurateur(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, "1789920900_droit_curateur.go")

	utilisateurs, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}
	if champ := utilisateurs.Fields.GetByName("curator"); champ != nil {
		t.Error("le champ curator survit au down")
	}
}
