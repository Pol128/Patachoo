// Package journal n'a aucun code de production : il n'existe que pour éprouver
// ./journal, le script qui assemble CHANGELOG.md à partir de changelog.d/. Il
// est donc lancé par `go test ./...`, et par ./verifie, sans rien demander de
// plus à l'environnement qu'un /bin/sh.
//
// Le script est joué pour de vrai, par /bin/sh, dans un répertoire temporaire
// qui porte un faux CHANGELOG.md et un faux changelog.d/ — jamais le journal du
// dépôt. Hors dépôt git, `git rm` échoue et le script retombe sur `rm` : c'est
// le chemin qu'on éprouve ici.
package journal

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ce qui précède « À paraître », et la version déjà publiée qui la suit : le
// script doit les recopier à l'octet près.
const (
	avant       = "# Journal des versions\n\nUne section par version.\n\n"
	anterieures = "## v0.1.0 — 2026-08-19\n\n- Première version.\n"
)

// publier pose `journal` (le CHANGELOG.md de départ) et les fragments dans un
// répertoire temporaire, y joue `./journal publier <version>`, et rend le
// CHANGELOG.md produit et le répertoire.
func publier(t *testing.T, journal string, fragments map[string]string, version string) (string, string) {
	t.Helper()
	script, err := filepath.Abs("../../journal")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(journal), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "changelog.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	fragments["LISEZ-MOI.md"] = "# Les entrées de journal en attente de publication\n"
	for nom, contenu := range fragments {
		if err := os.WriteFile(filepath.Join(dir, "changelog.d", nom), []byte(contenu), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("/bin/sh", script, "publier", version)
	cmd.Dir = dir
	var sortie bytes.Buffer
	cmd.Stdout, cmd.Stderr = &sortie, &sortie
	if err := cmd.Run(); err != nil {
		t.Fatalf("./journal publier %s : %v\n%s", version, err, sortie.String())
	}

	produit, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(produit), dir
}

// fragmentsRetires échoue si changelog.d/ porte autre chose que LISEZ-MOI.md.
func fragmentsRetires(t *testing.T, dir string) {
	t.Helper()
	entrees, err := os.ReadDir(filepath.Join(dir, "changelog.d"))
	if err != nil {
		t.Fatal(err)
	}
	var noms []string
	for _, e := range entrees {
		noms = append(noms, e.Name())
	}
	if strings.Join(noms, " ") != "LISEZ-MOI.md" {
		t.Errorf("changelog.d/ après publication : %v, attendu [LISEZ-MOI.md]", noms)
	}
}

// Le cas de toutes les publications depuis le découpage en fragments :
// « À paraître » ne porte que son placeholder, qui doit y rester et ne pas
// descendre sous la version ouverte.
func TestPublierLaisseLePlaceholderSousAParaitre(t *testing.T) {
	journal := avant + "## À paraître\n\n_Rien pour l'instant._\n\n" + anterieures
	produit, dir := publier(t, journal, map[string]string{
		"PATA-1.md": "- Puce de PATA-1.\n",
		"PATA-2.md": "- Puce de PATA-2.\n",
	}, "0.2.0")

	attendu := avant +
		"## À paraître\n\n_Rien pour l'instant._\n\n" +
		"## v0.2.0 — " + time.Now().Format("2006-01-02") + "\n\n" +
		"- Puce de PATA-2.\n- Puce de PATA-1.\n\n" +
		anterieures
	if produit != attendu {
		t.Errorf("CHANGELOG.md produit :\n%s\nattendu :\n%s", produit, attendu)
	}
	if c := strings.Count(produit, "_Rien pour l'instant._"); c != 1 {
		t.Errorf("le placeholder apparaît %d fois, attendu une seule", c)
	}
	fragmentsRetires(t, dir)
}

// Le cas de la v0.1.0 : « À paraître » portait encore des puces écrites à la
// main, sans placeholder. Elles descendent dans la version, à la suite des
// fragments, et la section repart avec son seul placeholder.
func TestPublierVerseLesPucesDeAParaitreDansLaVersion(t *testing.T) {
	journal := avant + "## À paraître\n\n- Puce écrite à la main.\n- Une autre.\n\n" + anterieures
	produit, dir := publier(t, journal, map[string]string{
		"PATA-3.md": "- Puce de PATA-3.\n",
	}, "0.2.0")

	attendu := avant +
		"## À paraître\n\n_Rien pour l'instant._\n\n" +
		"## v0.2.0 — " + time.Now().Format("2006-01-02") + "\n\n" +
		"- Puce de PATA-3.\n- Puce écrite à la main.\n- Une autre.\n\n" +
		anterieures
	if produit != attendu {
		t.Errorf("CHANGELOG.md produit :\n%s\nattendu :\n%s", produit, attendu)
	}
	fragmentsRetires(t, dir)
}
