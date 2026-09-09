package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// baseNeuve monte une base vide, y branche nos hooks puis applique les
// migrations — dans l'ordre où main() le fait.
//
// Ni serveur ni requête HTTP : la normalisation vit dans un hook de modèle,
// elle se déclenche donc sur app.Save. Un test qui passerait par le routeur
// vérifierait PocketBase, pas notre règle.
func baseNeuve(t *testing.T) core.App {
	t.Helper()

	return baseNeuveAvec(t, analyseurDeTest(t))
}

func tagNeuf(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("collection tags : %v", err)
	}
	return core.NewRecord(collection)
}

func nombreDeTags(t *testing.T, app core.App) int {
	t.Helper()

	tags, err := app.FindAllRecords("tags")
	if err != nil {
		t.Fatalf("lecture des tags : %v", err)
	}
	return len(tags)
}

func noms(tags []*core.Record) []string {
	resultat := make([]string, len(tags))
	for i, tag := range tags {
		resultat[i] = tag.GetString("name")
	}
	return resultat
}

// --- Le hook -------------------------------------------------------------

func TestLeHookNormaliseLeNomEtCalculeLeSlug(t *testing.T) {
	app := baseNeuve(t)

	cas := []struct{ saisi, nom, slug string }{
		{"  Végétarien  ", "végétarien", "vegetarien"},
		{"Plat   unique", "plat unique", "plat-unique"},
	}
	for _, c := range cas {
		t.Run(c.saisi, func(t *testing.T) {
			tag := tagNeuf(t, app)
			tag.Set("name", c.saisi)
			if err := app.Save(tag); err != nil {
				t.Fatalf("enregistrement de %q : %v", c.saisi, err)
			}
			if obtenu := tag.GetString("name"); obtenu != c.nom {
				t.Errorf("name = %q, attendu %q", obtenu, c.nom)
			}
			if obtenu := tag.GetString("slug"); obtenu != c.slug {
				t.Errorf("slug = %q, attendu %q", obtenu, c.slug)
			}
		})
	}
}

// Renommer sans recalculer laisserait un slug qui ne désigne plus rien, et
// deux tags renommés l'un vers l'autre ne se heurteraient jamais.
func TestLeHookRecalculeLeSlugAuRenommage(t *testing.T) {
	app := baseNeuve(t)

	tag := tagNeuf(t, app)
	tag.Set("name", "Printemps")
	if err := app.Save(tag); err != nil {
		t.Fatalf("création : %v", err)
	}

	tag.Set("name", "Été")
	if err := app.Save(tag); err != nil {
		t.Fatalf("renommage : %v", err)
	}
	if obtenu := tag.GetString("slug"); obtenu != "ete" {
		t.Errorf("slug après renommage = %q, attendu %q", obtenu, "ete")
	}
}

// Le slug n'est jamais lu de l'appelant : sinon un slug posté à la main
// contournerait l'index d'unicité, et deux « végétarien » cohabiteraient.
func TestLeHookEcraseUnSlugPoseALaMain(t *testing.T) {
	app := baseNeuve(t)

	tag := tagNeuf(t, app)
	tag.Set("name", "Végétarien")
	tag.Set("slug", "nimporte-quoi")
	if err := app.Save(tag); err != nil {
		t.Fatalf("enregistrement : %v", err)
	}
	if obtenu := tag.GetString("slug"); obtenu != "vegetarien" {
		t.Errorf("slug = %q, attendu %q", obtenu, "vegetarien")
	}
}

// Dans le sens du refus : « --- » ne donne aucun slug. L'index unique ne
// l'accepterait qu'une fois, avec un message que personne ne comprendrait.
func TestUnNomSansSlugEstRefuse(t *testing.T) {
	app := baseNeuve(t)

	tag := tagNeuf(t, app)
	tag.Set("name", "---")

	err := app.Save(tag)
	if err == nil {
		t.Fatal("« --- » a été accepté, il ne donne pourtant aucun slug")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("l'erreur ne nomme pas le champ fautif : %v", err)
	}
	if n := nombreDeTags(t, app); n != 0 {
		t.Errorf("%d tag(s) en base, attendu 0", n)
	}
}

// Le comportement voulu, et la raison d'être de tagsDepuisSaisie : enregistrer
// en direct ne déduplique pas, il se heurte à l'index.
func TestUnSecondTagDeMemeSlugEstRefuse(t *testing.T) {
	app := baseNeuve(t)

	premier := tagNeuf(t, app)
	premier.Set("name", "végétarien")
	if err := app.Save(premier); err != nil {
		t.Fatalf("premier tag : %v", err)
	}

	second := tagNeuf(t, app)
	second.Set("name", "VÉGÉTARIEN")
	if err := app.Save(second); err == nil {
		t.Error("« VÉGÉTARIEN » a été accepté alors que « végétarien » existe")
	}
	if n := nombreDeTags(t, app); n != 1 {
		t.Errorf("%d tag(s) en base, attendu 1", n)
	}
}

// --- tagsDepuisSaisie ----------------------------------------------------

func TestTagsDepuisSaisieCreeDansLOrdreDeLaSaisie(t *testing.T) {
	app := baseNeuve(t)

	tags, err := tagsDepuisSaisie(app, "Végétarien, plat unique")
	if err != nil {
		t.Fatalf("saisie refusée : %v", err)
	}
	attendu := []string{"végétarien", "plat unique"}
	if obtenu := noms(tags); !memeSuite(obtenu, attendu) {
		t.Errorf("noms = %v, attendu %v", obtenu, attendu)
	}
	if n := nombreDeTags(t, app); n != 2 {
		t.Errorf("%d tag(s) en base, attendu 2", n)
	}
}

func TestTagsDepuisSaisieReutiliseUnSlugExistant(t *testing.T) {
	app := baseNeuve(t)

	premiers, err := tagsDepuisSaisie(app, "Végétarien, plat unique")
	if err != nil {
		t.Fatalf("première saisie : %v", err)
	}

	seconds, err := tagsDepuisSaisie(app, "vegetarien")
	if err != nil {
		t.Fatalf("seconde saisie : %v", err)
	}
	if len(seconds) != 1 {
		t.Fatalf("%d tag(s) rendu(s), attendu 1", len(seconds))
	}
	if seconds[0].Id != premiers[0].Id {
		t.Errorf("identifiant %q, attendu %q : le tag existant n'a pas été réutilisé",
			seconds[0].Id, premiers[0].Id)
	}
	if n := nombreDeTags(t, app); n != 2 {
		t.Errorf("%d tag(s) en base, attendu 2", n)
	}
}

func TestTagsDepuisSaisieReplieLesDoublonsSurLeSlug(t *testing.T) {
	app := baseNeuve(t)

	tags, err := tagsDepuisSaisie(app, "Végétarien, VÉGÉTARIEN , vegetarien")
	if err != nil {
		t.Fatalf("saisie refusée : %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("%d tag(s) rendu(s), attendu 1 : %v", len(tags), noms(tags))
	}
	if n := nombreDeTags(t, app); n != 1 {
		t.Errorf("%d tag(s) en base, attendu 1", n)
	}
}

// Une virgule en trop ne doit pas casser un formulaire.
func TestTagsDepuisSaisieIgnoreLesFragmentsSansSlug(t *testing.T) {
	app := baseNeuve(t)

	tags, err := tagsDepuisSaisie(app, "vegetarien,, ,  ")
	if err != nil {
		t.Fatalf("saisie refusée : %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("%d tag(s) rendu(s), attendu 1 : %v", len(tags), noms(tags))
	}

	tousLesTags, err := app.FindAllRecords("tags")
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range tousLesTags {
		if tag.GetString("slug") == "" {
			t.Errorf("un tag à slug vide a été créé (name = %q)", tag.GetString("name"))
		}
	}
}

func TestTagsDepuisSaisieVideRendUneListeVide(t *testing.T) {
	app := baseNeuve(t)

	tags, err := tagsDepuisSaisie(app, "")
	if err != nil {
		t.Fatalf("une saisie vide n'est pas une erreur : %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("%d tag(s) rendu(s), attendu 0", len(tags))
	}
	if n := nombreDeTags(t, app); n != 0 {
		t.Errorf("%d tag(s) en base, attendu 0", n)
	}
}

func TestTagsDepuisSaisieConserveLOrdre(t *testing.T) {
	app := baseNeuve(t)

	tags, err := tagsDepuisSaisie(app, "c, a, b")
	if err != nil {
		t.Fatalf("saisie refusée : %v", err)
	}
	attendu := []string{"c", "a", "b"}
	if obtenu := noms(tags); !memeSuite(obtenu, attendu) {
		t.Errorf("noms = %v, attendu %v", obtenu, attendu)
	}
}

// recipes.tags est borné à 20 par le schéma. Les deux sens ont leur test :
// sans le refus, la saisie créerait des tags qu'aucune recette ne pourrait
// porter ; sans l'acceptation, la borne serait posée un cran trop bas.
func TestTagsDepuisSaisieAccepteVingtTags(t *testing.T) {
	app := baseNeuve(t)

	tags, err := tagsDepuisSaisie(app, saisieDeNTags(20))
	if err != nil {
		t.Fatalf("20 tags refusés : %v", err)
	}
	if len(tags) != 20 {
		t.Errorf("%d tag(s) rendu(s), attendu 20", len(tags))
	}
	if n := nombreDeTags(t, app); n != 20 {
		t.Errorf("%d tag(s) en base, attendu 20", n)
	}
}

// Un tag peut échouer à l'écriture après que les précédents sont passés :
// une insertion concurrente du même slug, glissée entre la recherche et
// l'enregistrement, suffit. Sans transaction, la saisie laisserait derrière
// elle les tags créés avant l'échec, que rien ne référencerait.
//
// L'échec est provoqué par un hook plutôt que par une course : le
// comportement vérifié est le retour en arrière, pas le hasard qui l'appelle.
func TestUneSaisieQuiEchoueEnCoursNeLaisseRienDerriere(t *testing.T) {
	app := baseNeuve(t)

	app.OnRecordCreate("tags").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("slug") == "boum" {
			return errors.New("écriture refusée pour le test")
		}
		return e.Next()
	})

	if _, err := tagsDepuisSaisie(app, "a, b, boum"); err == nil {
		t.Fatal("la saisie a été acceptée alors qu'un tag ne peut pas s'écrire")
	}
	if n := nombreDeTags(t, app); n != 0 {
		t.Errorf("%d tag(s) en base après un échec, attendu 0", n)
	}
}

func TestTagsDepuisSaisieRefuseVingtEtUnTags(t *testing.T) {
	app := baseNeuve(t)

	if _, err := tagsDepuisSaisie(app, saisieDeNTags(21)); err == nil {
		t.Error("21 tags acceptés, la borne du schéma est de 20")
	}
	// Créer d'abord pour échouer ensuite laisserait des tags orphelins.
	if n := nombreDeTags(t, app); n != 0 {
		t.Errorf("%d tag(s) en base après un refus, attendu 0", n)
	}
}

func saisieDeNTags(n int) string {
	fragments := make([]string, n)
	for i := range fragments {
		fragments[i] = fmt.Sprintf("tag %d", i)
	}
	return strings.Join(fragments, ", ")
}

func memeSuite(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- Le nom du tag filtré --------------------------------------------------

// Une panne de lecture n'est pas un tag inconnu. Replier sur le slug pour
// toute erreur rendrait une page normale, dont le bandeau nomme le slug comme
// si le tag n'existait pas, alors que la base ne répond plus : un mensonge
// tranquille au lieu d'une erreur.
//
// Le repli sur le slug en cas d'absence, lui, est du comportement voulu, et il
// est couvert là où il se voit — TestLeFiltreEchappeLeTagRecuDansLURL, dans
// tags_vue_test.go.
func TestNomDuTagRemonteUnePanneDeLecture(t *testing.T) {
	app := baseNeuve(t)

	if _, err := app.DB().NewQuery("DROP TABLE tags").Execute(); err != nil {
		t.Fatalf("suppression de la table tags : %v", err)
	}

	nom, err := nomDuTag(app, "vegetarien")
	if err == nil {
		t.Fatalf("panne de lecture acceptée, nom rendu %q", nom)
	}
	if errors.Is(err, sql.ErrNoRows) {
		t.Errorf("panne de lecture prise pour une absence de résultat : %v", err)
	}
	if nom == "vegetarien" {
		t.Errorf("le slug est rendu malgré la panne : %q", nom)
	}
}
