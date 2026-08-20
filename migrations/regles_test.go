package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// Les deux règles de PATA-35, littéralement.
//
// Le premier terme de la règle de suppression n'est pas décoratif :
// created_by n'est pas Required, une recette peut donc le porter vide —
// importée avant cette tâche, ou créée par un compte supprimé depuis. Réduite
// à « created_by = @request.auth.id », la règle comparerait "" à "" pour une
// requête non authentifiée, et lui accorderait la suppression.
const (
	regleCompteConnecte = `@request.auth.id != ""`
	regleAuteurSeul     = `@request.auth.id != "" && created_by = @request.auth.id`
)

// reglesAttendues : les dix règles, par collection puis par verbe.
//
// Un ingrédient n'a pas d'auteur propre : il fait partie d'une recette que
// tout compte connecté peut modifier, et retirer une ligne est une
// modification. La suppression de la recette les emporte déjà par cascade.
var reglesAttendues = map[string]map[string]string{
	"recipes": {
		"list":   regleCompteConnecte,
		"view":   regleCompteConnecte,
		"create": regleCompteConnecte,
		"update": regleCompteConnecte,
		"delete": regleAuteurSeul,
	},
	"ingredients": {
		"list":   regleCompteConnecte,
		"view":   regleCompteConnecte,
		"create": regleCompteConnecte,
		"update": regleCompteConnecte,
		"delete": regleCompteConnecte,
	},
}

// La comparaison est littérale, caractère pour caractère : une règle
// « équivalente » réécrite à la main est une règle qu'il faut relire, et
// celle-ci est la seule chose qui sépare une recette d'un compte qui n'y a pas
// droit.
func TestLesReglesDAccesSontPosees(t *testing.T) {
	app := baseNeuve(t)

	for nom, attendues := range reglesAttendues {
		posees := reglesDe(t, app, nom)
		for verbe, attendue := range attendues {
			posee := posees[verbe]
			if posee == nil {
				t.Errorf("%s.%sRule est nil : la collection reste réservée au superuser", nom, verbe)
				continue
			}
			if *posee != attendue {
				t.Errorf("%s.%sRule = %q, attendu %q", nom, verbe, *posee, attendue)
			}
		}
	}
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer.
func TestLeDownRemetLesReglesANil(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, "1787242977_regles_acces.go")

	for nom := range reglesAttendues {
		for verbe, regle := range reglesDe(t, app, nom) {
			if regle != nil {
				t.Errorf("%s.%sRule = %q après le down, attendu nil", nom, verbe, *regle)
			}
		}
	}
}

// Les règles livrées par PocketBase sur users ferment la collection sur
// elle-même : on ne voit que son propre compte. Elles restent ainsi. Les
// élargir pour peupler un « ajoutée par X » exposerait le courriel de tous les
// comptes par l'API REST ; le rendu étant serveur, la fiche résout le nom par
// app.FindRecordById, qui ne passe pas par les règles.
//
// Ce test rougira le jour où PATA-37 verrouillera createRule pour tenir
// l'inscription fermée : c'est voulu, un tel changement se décide, il ne se
// constate pas.
func TestLesReglesDeUsersSontInchangees(t *testing.T) {
	app := baseNeuve(t)

	proprietaire := "id = @request.auth.id"
	attendues := map[string]string{
		"list":   proprietaire,
		"view":   proprietaire,
		"create": "",
		"update": proprietaire,
		"delete": proprietaire,
	}

	posees := reglesDe(t, app, "users")
	for verbe, attendue := range attendues {
		posee := posees[verbe]
		if posee == nil {
			t.Errorf("users.%sRule est nil, attendu %q", verbe, attendue)
			continue
		}
		if *posee != attendue {
			t.Errorf("users.%sRule = %q, attendu %q", verbe, *posee, attendue)
		}
	}
}

// Le sens du refus : ce qu'un compte ne peut pas faire. Sans authentification,
// rien — pas même lire.
func TestUnVisiteurNePeutRienSurUneRecette(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteDe(t, app, compteNeuf(t, app, "auteur@exemple.test"))

	for verbe, regle := range reglesDe(t, app, "recipes") {
		if peutAcceder(t, app, recette, nil, regle) {
			t.Errorf("un visiteur non authentifié obtient %s sur une recette", verbe)
		}
	}
}

// La décision du 18/08/2026 : on modifie à plusieurs, on ne supprime que le
// sien. L'autorisation de modifier est donc affirmée ici, pour qu'elle ne
// passe pas un jour pour un oubli qu'on « corrigerait ».
func TestUnAutreCompteModifieMaisNeSupprimePas(t *testing.T) {
	app := baseNeuve(t)
	a := compteNeuf(t, app, "a@exemple.test")
	b := compteNeuf(t, app, "b@exemple.test")
	recette := recetteDe(t, app, a)

	regles := reglesDe(t, app, "recipes")
	if !peutAcceder(t, app, recette, b, regles["update"]) {
		t.Error("le compte B ne peut pas modifier la recette de A : les recettes sont partagées")
	}
	if peutAcceder(t, app, recette, b, regles["delete"]) {
		t.Error("le compte B supprime la recette de A")
	}
}

// Le test qui garde le premier terme de la règle de suppression.
func TestUneRecetteSansAuteurNeSeSupprimePasEnVisiteur(t *testing.T) {
	app := baseNeuve(t)
	recette := recetteDe(t, app, nil)

	if peutAcceder(t, app, recette, nil, reglesDe(t, app, "recipes")["delete"]) {
		t.Error("un visiteur supprime une recette dont created_by est vide : " +
			"la règle compare \"\" à \"\" et lui donne raison")
	}
}

// reglesDe rend les cinq règles d'une collection, indexées par verbe.
func reglesDe(t *testing.T, app core.App, nom string) map[string]*string {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId(nom)
	if err != nil {
		t.Fatalf("collection %s : %v", nom, err)
	}
	return map[string]*string{
		"list":   collection.ListRule,
		"view":   collection.ViewRule,
		"create": collection.CreateRule,
		"update": collection.UpdateRule,
		"delete": collection.DeleteRule,
	}
}

// peutAcceder évalue une règle contre un appelant, sans monter de serveur
// HTTP. Un compte nil est le visiteur anonyme.
func peutAcceder(t *testing.T, app core.App, enregistrement *core.Record, compte *core.Record, regle *string) bool {
	t.Helper()

	ok, err := app.CanAccessRecord(enregistrement, &core.RequestInfo{Auth: compte}, regle)
	if err != nil {
		t.Fatalf("évaluation de la règle : %v", err)
	}
	return ok
}

func compteNeuf(t *testing.T, app core.App, courriel string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}
	compte := core.NewRecord(collection)
	compte.SetEmail(courriel)
	compte.SetPassword("mot-de-passe-de-test")
	if err := app.Save(compte); err != nil {
		t.Fatalf("enregistrement du compte %s : %v", courriel, err)
	}
	return compte
}

// recetteDe enregistre une recette attribuée au compte donné — ou sans auteur
// du tout si celui-ci est nil, ce qui est un état que la base porte réellement.
func recetteDe(t *testing.T, app core.App, auteur *core.Record) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", "Tarte aux pommes")
	if auteur != nil {
		recette.Set("created_by", auteur.Id)
	}
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette : %v", err)
	}
	return recette
}
