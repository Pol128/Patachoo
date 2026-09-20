package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// --- Montage -------------------------------------------------------------

// tagEnregistre crée un tag et rend son enregistrement. Le hook de PATA-32
// pose le slug : c'est lui la clé, et c'est lui qu'on fusionne.
func tagEnregistre(t *testing.T, app core.App, nom string) *core.Record {
	t.Helper()

	tag := tagNeuf(t, app)
	tag.Set("name", nom)
	if err := app.Save(tag); err != nil {
		t.Fatalf("enregistrement du tag %q : %v", nom, err)
	}
	return tag
}

func recetteAvecTags(t *testing.T, app core.App, titre string, tags ...*core.Record) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		t.Fatalf("collection recipes : %v", err)
	}
	recette := core.NewRecord(collection)
	recette.Set("title", titre)
	recette.Set("tags", identifiants(tags))
	if err := app.Save(recette); err != nil {
		t.Fatalf("enregistrement de la recette %q : %v", titre, err)
	}
	return recette
}

func identifiants(tags []*core.Record) []string {
	ids := make([]string, len(tags))
	for i, tag := range tags {
		ids[i] = tag.Id
	}
	return ids
}

// slugsDeLaRecette relit la recette en base et rend les slugs de ses tags,
// dans l'ordre. Relire plutôt qu'interroger l'enregistrement en mémoire : une
// transaction annulée laisse ce dernier tel que le code l'avait modifié.
func slugsDeLaRecette(t *testing.T, app core.App, id string) []string {
	t.Helper()

	recette, err := app.FindRecordById("recipes", id)
	if err != nil {
		t.Fatalf("relecture de la recette %q : %v", id, err)
	}

	tags, err := app.FindAllRecords("tags")
	if err != nil {
		t.Fatalf("lecture des tags : %v", err)
	}
	parID := make(map[string]string, len(tags))
	for _, tag := range tags {
		parID[tag.Id] = tag.GetString("slug")
	}

	slugs := make([]string, 0, len(recette.GetStringSlice("tags")))
	for _, tagID := range recette.GetStringSlice("tags") {
		slug, connu := parID[tagID]
		if !connu {
			// Une référence vers un tag disparu : à signaler, pas à taire.
			slug = "tag inconnu " + tagID
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

func tagExiste(t *testing.T, app core.App, slug string) bool {
	t.Helper()

	tags, err := app.FindAllRecords("tags")
	if err != nil {
		t.Fatalf("lecture des tags : %v", err)
	}
	for _, tag := range tags {
		if tag.GetString("slug") == slug {
			return true
		}
	}
	return false
}

func occurrences(slugs []string, cherche string) int {
	n := 0
	for _, slug := range slugs {
		if slug == cherche {
			n++
		}
	}
	return n
}

// sans rend les slugs privés de celui donné, dans l'ordre : c'est ce qui
// permet de dire « les autres tags sont intacts » sans figer la place que la
// cible occupe, que la tâche ne fixe pas.
func sans(slugs []string, exclu string) []string {
	restants := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		if slug != exclu {
			restants = append(restants, slug)
		}
	}
	return restants
}

// executeLaCommande monte une racine cobra neuve, y branche nos commandes et
// exécute les arguments donnés. La sortie standard et la sortie d'erreur sont
// capturées ensemble : le compte rendu se lit d'un bloc.
//
// aEchoue rend le témoin que brancheLesCommandes laisse à main() : c'est lui
// qui porte le code de retour du binaire.
func executeLaCommande(t *testing.T, app core.App, args ...string) (sortie string, aEchoue bool, err error) {
	t.Helper()

	racine := &cobra.Command{Use: "patachoo"}
	echoue := brancheLesCommandes(app, racine, analyseurDeTest(t))

	var tampon bytes.Buffer
	racine.SetOut(&tampon)
	racine.SetErr(&tampon)
	racine.SetArgs(args)

	err = racine.Execute()
	return tampon.String(), echoue(), err
}

// --- fusionnerTags -------------------------------------------------------

// Le cas nominal : trois recettes portent « a », une quatrième porte déjà
// « b ». Après la fusion, les quatre se rejoignent sur « b » et « a » a
// disparu de la collection — c'est tout l'objet de la commande.
func TestFusionnerTagsReporteLesRecettesSurLaCible(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	b := tagEnregistre(t, app, "b")
	premiere := recetteAvecTags(t, app, "première", a)
	deuxieme := recetteAvecTags(t, app, "deuxième", a)
	troisieme := recetteAvecTags(t, app, "troisième", a)
	quatrieme := recetteAvecTags(t, app, "quatrième", b)

	modifiees, err := fusionnerTags(app, "a", "b")
	if err != nil {
		t.Fatalf("fusion refusée : %v", err)
	}
	// Les recettes modifiées sont celles qui portaient « a » : la quatrième
	// portait déjà « b », la fusion ne la touche pas.
	if modifiees != 3 {
		t.Errorf("%d recette(s) modifiée(s), attendu 3", modifiees)
	}

	for _, recette := range []*core.Record{premiere, deuxieme, troisieme, quatrieme} {
		slugs := slugsDeLaRecette(t, app, recette.Id)
		if occurrences(slugs, "b") != 1 {
			t.Errorf("recette %q : tags = %v, attendu un « b »", recette.GetString("title"), slugs)
		}
		if occurrences(slugs, "a") != 0 {
			t.Errorf("recette %q : tags = %v, « a » n'a pas été retiré", recette.GetString("title"), slugs)
		}
	}

	if tagExiste(t, app, "a") {
		t.Error("le tag « a » existe encore après la fusion")
	}
}

// Une recette qui portait les deux tags ne doit pas ressortir avec « b » en
// double : la relation accepterait le doublon, l'affichage le montrerait.
func TestFusionnerTagsNAjoutePasLaCibleEnDouble(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	b := tagEnregistre(t, app, "b")
	recette := recetteAvecTags(t, app, "les deux", a, b)

	if _, err := fusionnerTags(app, "a", "b"); err != nil {
		t.Fatalf("fusion refusée : %v", err)
	}

	slugs := slugsDeLaRecette(t, app, recette.Id)
	if occurrences(slugs, "b") != 1 {
		t.Errorf("tags = %v, attendu un seul « b »", slugs)
	}
}

// La fusion ne touche qu'à un tag : les autres restent, et dans leur ordre.
// Sans ça, une fusion réordonnerait silencieusement toutes les fiches.
func TestFusionnerTagsLaisseLesAutresTagsIntacts(t *testing.T) {
	app := baseNeuve(t)

	x := tagEnregistre(t, app, "x")
	a := tagEnregistre(t, app, "a")
	y := tagEnregistre(t, app, "y")
	tagEnregistre(t, app, "b")
	recette := recetteAvecTags(t, app, "entourée", x, a, y)

	if _, err := fusionnerTags(app, "a", "b"); err != nil {
		t.Fatalf("fusion refusée : %v", err)
	}

	slugs := slugsDeLaRecette(t, app, recette.Id)
	attendu := []string{"x", "y"}
	if autres := sans(slugs, "b"); !memeSuite(autres, attendu) {
		t.Errorf("tags hors cible = %v, attendu %v (tags = %v)", autres, attendu, slugs)
	}
	if occurrences(slugs, "b") != 1 {
		t.Errorf("tags = %v, attendu un « b »", slugs)
	}
}

// Dans le sens du refus : un slug source inconnu n'écrit rien. Une fusion
// lancée sur une faute de frappe ne doit pas commencer par supprimer un tag.
func TestFusionnerTagsRefuseUnSlugSourceInconnu(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	tagEnregistre(t, app, "b")
	recette := recetteAvecTags(t, app, "intacte", a)

	_, err := fusionnerTags(app, "absent", "b")
	if err == nil {
		t.Fatal("une source inconnue a été acceptée")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("l'erreur ne nomme pas le slug fautif : %v", err)
	}
	if slugs := slugsDeLaRecette(t, app, recette.Id); !memeSuite(slugs, []string{"a"}) {
		t.Errorf("tags = %v, attendu [a] : la recette a été modifiée", slugs)
	}
	if n := nombreDeTags(t, app); n != 2 {
		t.Errorf("%d tag(s) en base, attendu 2", n)
	}
}

// Le même refus dans l'autre sens : reporter des recettes sur un tag qui
// n'existe pas les laisserait sans tag du tout.
func TestFusionnerTagsRefuseUnSlugCibleInconnu(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	tagEnregistre(t, app, "b")
	recette := recetteAvecTags(t, app, "intacte", a)

	_, err := fusionnerTags(app, "a", "absent")
	if err == nil {
		t.Fatal("une cible inconnue a été acceptée")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("l'erreur ne nomme pas le slug fautif : %v", err)
	}
	if slugs := slugsDeLaRecette(t, app, recette.Id); !memeSuite(slugs, []string{"a"}) {
		t.Errorf("tags = %v, attendu [a] : la recette a été modifiée", slugs)
	}
	if n := nombreDeTags(t, app); n != 2 {
		t.Errorf("%d tag(s) en base, attendu 2", n)
	}
}

// Fusionner un tag avec lui-même reviendrait à le retirer des recettes puis à
// le supprimer : une perte silencieuse, pas une opération nulle.
func TestFusionnerTagsRefuseUneSourceEgaleALaCible(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	recette := recetteAvecTags(t, app, "intacte", a)

	_, err := fusionnerTags(app, "a", "a")
	if err == nil {
		t.Fatal("« a » vers « a » a été accepté")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("l'erreur ne nomme pas le slug fautif : %v", err)
	}
	if !tagExiste(t, app, "a") {
		t.Error("le tag « a » a été supprimé")
	}
	if slugs := slugsDeLaRecette(t, app, recette.Id); !memeSuite(slugs, []string{"a"}) {
		t.Errorf("tags = %v, attendu [a]", slugs)
	}
}

// Le test de la transaction, et sans lui elle peut manquer sans que rien ne
// rougisse : une fusion à moitié faite ne se voit pas, et personne ne saurait
// laquelle des recettes reste à reprendre.
//
// L'échec est provoqué sur la deuxième mise à jour, quel que soit l'ordre dans
// lequel les recettes remontent : une au moins a donc déjà été enregistrée
// quand il survient. C'est ce qui rend le retour en arrière observable.
func TestUneFusionQuiEchoueEnCoursNeLaisseRienDerriere(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	tagEnregistre(t, app, "b")
	recettes := []*core.Record{
		recetteAvecTags(t, app, "première", a),
		recetteAvecTags(t, app, "deuxième", a),
		recetteAvecTags(t, app, "troisième", a),
	}

	misesAJour := 0
	app.OnRecordUpdate("recipes").BindFunc(func(e *core.RecordEvent) error {
		misesAJour++
		if misesAJour == 2 {
			return errors.New("mise à jour refusée pour le test")
		}
		return e.Next()
	})

	if _, err := fusionnerTags(app, "a", "b"); err == nil {
		t.Fatal("la fusion a été acceptée alors qu'une recette ne peut pas s'écrire")
	}

	for _, recette := range recettes {
		slugs := slugsDeLaRecette(t, app, recette.Id)
		if !memeSuite(slugs, []string{"a"}) {
			t.Errorf("recette %q : tags = %v, attendu [a] après l'échec",
				recette.GetString("title"), slugs)
		}
	}
	if !tagExiste(t, app, "a") {
		t.Error("le tag « a » a été supprimé alors que la fusion a échoué")
	}
}

// --- La commande ---------------------------------------------------------

// Une opération irréversible qui s'exécute au premier essai est une opération
// qu'on regrette : sans --appliquer, la commande ne fait que compter.
func TestLaCommandeNecritRienSansAppliquer(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	tagEnregistre(t, app, "b")
	recette := recetteAvecTags(t, app, "intacte", a)

	sortie, _, err := executeLaCommande(t, app, "tags", "fusionner", "a", "b")
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}

	if slugs := slugsDeLaRecette(t, app, recette.Id); !memeSuite(slugs, []string{"a"}) {
		t.Errorf("tags = %v, attendu [a] : la commande a écrit sans --appliquer", slugs)
	}
	if !tagExiste(t, app, "a") {
		t.Error("le tag « a » a été supprimé sans --appliquer")
	}
	if !strings.Contains(sortie, "1") {
		t.Errorf("la sortie n'annonce pas le nombre de recettes concernées :\n%s", sortie)
	}
}

func TestLaCommandeEcritAvecAppliquer(t *testing.T) {
	app := baseNeuve(t)

	a := tagEnregistre(t, app, "a")
	tagEnregistre(t, app, "b")
	recette := recetteAvecTags(t, app, "fusionnée", a)

	sortie, aEchoue, err := executeLaCommande(t, app, "tags", "fusionner", "a", "b", "--appliquer")
	if err != nil {
		t.Fatalf("commande en erreur : %v\n%s", err, sortie)
	}
	// Le sens qui manque au test du slug inconnu : une fusion qui aboutit ne
	// doit pas faire sortir le binaire en erreur.
	if aEchoue {
		t.Error("une fusion réussie est signalée comme un échec au binaire")
	}

	if slugs := slugsDeLaRecette(t, app, recette.Id); !memeSuite(slugs, []string{"b"}) {
		t.Errorf("tags = %v, attendu [b]", slugs)
	}
	if tagExiste(t, app, "a") {
		t.Error("le tag « a » existe encore après --appliquer")
	}
	if !strings.Contains(sortie, "1") {
		t.Errorf("la sortie n'annonce pas le nombre de recettes concernées :\n%s", sortie)
	}
}

// Une erreur doit remonter : c'est elle qui donne le code de retour non nul
// une fois la commande branchée sur le binaire.
func TestLaCommandeRendUneErreurSurUnSlugInconnu(t *testing.T) {
	app := baseNeuve(t)

	tagEnregistre(t, app, "b")

	sortie, aEchoue, err := executeLaCommande(t, app, "tags", "fusionner", "absent", "b", "--appliquer")
	if err == nil {
		t.Fatalf("un slug inconnu n'a pas fait échouer la commande :\n%s", sortie)
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("l'erreur ne nomme pas le slug fautif : %v", err)
	}
	// PocketBase ignore délibérément l'erreur rendue par la racine cobra :
	// sans ce témoin, une fusion refusée s'arrêterait sur un code de retour
	// nul, et le script qui l'appelle la croirait passée.
	if !aEchoue {
		t.Error("l'échec n'est pas signalé au binaire : le code de retour resterait nul")
	}
}
