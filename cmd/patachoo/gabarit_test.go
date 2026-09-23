package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// migrationsAppliquees rend le journal des migrations de la base, fichier et
// horodatage d'application, dans l'ordre où elles ont été jouées.
func migrationsAppliquees(t *testing.T, app core.App) []dbx.NullStringMap {
	t.Helper()

	var lignes []dbx.NullStringMap
	if err := app.DB().NewQuery("SELECT file, applied FROM _migrations ORDER BY rowid").All(&lignes); err != nil {
		t.Fatalf("lecture de _migrations : %v", err)
	}
	if len(lignes) == 0 {
		t.Fatal("aucune migration appliquée : la base n'est pas montée")
	}
	return lignes
}

func memesMigrations(a, b []dbx.NullStringMap) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i]["file"].String != b[i]["file"].String || a[i]["applied"].String != b[i]["applied"].String {
			return false
		}
	}
	return true
}

// L'horodatage d'application est pris à la microseconde par PocketBase : deux
// bases migrées chacune de son côté ne le partagent pas. Deux bases qui le
// partagent ont été recopiées de la même — c'est la trace du gabarit, lue dans
// la base elle-même plutôt que dans une durée.
func TestLesBasesDeTestSontRecopieesDUnGabaritMigreUneFois(t *testing.T) {
	a := migrationsAppliquees(t, baseNeuve(t))
	b := migrationsAppliquees(t, baseNeuve(t))

	if !memesMigrations(a, b) {
		t.Errorf("deux bases de test aux migrations horodatées différemment : chacune a été "+
			"migrée pour elle-même, au lieu d'être recopiée du gabarit\n  %v\n  %v", a[0], b[0])
	}
}

// La recopie ne doit rien coûter au facteur bcrypt : le gabarit le porte déjà.
func TestLaBaseRecopieePorteLeCoutBcryptDesTests(t *testing.T) {
	app := baseNeuve(t)

	for _, nom := range []string{"users", core.CollectionNameSuperusers} {
		collection, err := app.FindCollectionByNameOrId(nom)
		if err != nil {
			t.Fatalf("collection %s : %v", nom, err)
		}
		champ := collection.Fields.GetByName(core.FieldNamePassword).(*core.PasswordField)
		if champ.Cost != coutBcryptDesTests {
			t.Errorf("collection %s : coût bcrypt %d, attendu %d", nom, champ.Cost, coutBcryptDesTests)
		}
	}
}

// Le gabarit mutualise la migration, pas la base : ce qu'un test écrit dans la
// sienne ne se lit pas dans celle du suivant.
func TestDeuxBasesDeTestNePartagentRien(t *testing.T) {
	a := baseNeuve(t)
	b := baseNeuve(t)

	if a.DataDir() == b.DataDir() {
		t.Fatalf("deux bases de test dans le même répertoire : %s", a.DataDir())
	}
	tagNeuf(t, a)

	tags, err := b.FindAllRecords("tags")
	if err != nil {
		t.Fatalf("lecture des tags : %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("%d tag(s) dans une base où personne n'en a écrit : les deux bases se partagent un fichier", len(tags))
	}
}

var gabaritAnnonce = regexp.MustCompile(`gabarit de base construit dans (\S+)`)

// Le gabarit se construit dans un répertoire temporaire à chaque passe, et
// c'est TestMain qui le retire une fois les tests joués. Le binaire de test est
// relancé seul, avec un répertoire temporaire à lui : ce qui y reste après sa
// sortie est ce qu'une passe laisse derrière elle.
func TestAucunGabaritNeSurvitALaPasse(t *testing.T) {
	temporaire := t.TempDir()

	cmd := exec.Command(os.Args[0], "-test.run=^TestDeuxBasesDeTestNePartagentRien$",
		"-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "TMPDIR="+temporaire)
	sortie, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("la passe relancée a échoué : %v\n%s", err, sortie)
	}

	annonce := gabaritAnnonce.FindSubmatch(sortie)
	if annonce == nil {
		t.Fatalf("la passe relancée n'a construit aucun gabarit :\n%s", sortie)
	}
	if dir := string(annonce[1]); !strings.HasPrefix(dir, temporaire+string(filepath.Separator)) {
		t.Fatalf("gabarit construit dans %s, hors du répertoire temporaire de la passe %s", dir, temporaire)
	}

	restes, err := os.ReadDir(temporaire)
	if err != nil {
		t.Fatalf("lecture de %s : %v", temporaire, err)
	}
	for _, reste := range restes {
		t.Errorf("%s survit à la passe", filepath.Join(temporaire, reste.Name()))
	}
}
