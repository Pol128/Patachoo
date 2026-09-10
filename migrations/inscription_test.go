package migrations

import (
	"testing"
)

// PATA-37. Le défaut d'un réglage de sécurité se choisit du côté qui refuse :
// une instance fraîche s'installe porte fermée, et c'est l'hébergeant qui
// l'ouvre depuis /_/ quand il le décide.
func TestLaCollectionDesReglagesArriveAvecSonUniqueEnregistrement(t *testing.T) {
	app := baseNeuve(t)

	reglages, err := app.FindAllRecords("settings")
	if err != nil {
		t.Fatalf("lecture de settings : %v", err)
	}
	if len(reglages) != 1 {
		t.Fatalf("%d enregistrements dans settings, attendu 1", len(reglages))
	}
	if reglages[0].GetBool("open_registration") {
		t.Error("open_registration est vrai sur une base fraîche : " +
			"l'instance s'installerait porte ouverte, en retard sur le premier passant")
	}
}

// Le réglage se lit depuis notre code, qui ne passe pas par les règles, et se
// modifie depuis l'administration, qui ne les lit pas non plus. Une règle
// posée ici n'ouvrirait donc que l'API REST, à personne d'utile.
func TestLesReglesDesReglagesSontToutesNil(t *testing.T) {
	app := baseNeuve(t)

	for verbe, regle := range reglesDe(t, app, "settings") {
		if regle != nil {
			t.Errorf("settings.%sRule = %q, attendu nil", verbe, *regle)
		}
	}
}

// Le test qui ferme la porte de derrière : PocketBase livre users avec une
// createRule vide, donc publique, et n'importe qui peut se créer un compte par
// POST /api/collections/users/records sans passer par la moindre page à nous.
func TestLaCreationDUnCompteEstReserveeAuSuperuser(t *testing.T) {
	app := baseNeuve(t)

	if regle := reglesDe(t, app, "users")["create"]; regle != nil {
		t.Errorf("users.createRule = %q, attendu nil : l'inscription a une seconde porte", *regle)
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer. Le down rend l'état d'avant,
// createRule vide comprise — et non nil, qui est autre chose.
func TestLeDownRetireLesReglagesEtRouvreLaCreationDeCompte(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, "1788994542_inscription.go")

	if _, err := app.FindCollectionByNameOrId("settings"); err == nil {
		t.Error("la collection settings survit au down")
	}

	regle := reglesDe(t, app, "users")["create"]
	if regle == nil {
		t.Fatal("users.createRule est nil après le down, attendu \"\" — sa valeur d'origine")
	}
	if *regle != "" {
		t.Errorf("users.createRule = %q après le down, attendu \"\"", *regle)
	}
}
