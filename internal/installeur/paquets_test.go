package installeur

// Les paquets .deb et .rpm, éprouvés par ce qui s'en lit sans les construire :
// l'unité qu'ils posent, et leurs scripts de maintenance joués par /bin/sh.
// nfpm, dpkg et rpm n'y sont pas — leur preuve est la transcription en
// conteneurs de la demande de fusion qui les a introduits (PATA-25).

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// directives rend les lignes significatives d'une unité systemd : en-têtes de
// section et directives, sans les commentaires ni les lignes vides, dans leur
// ordre. ExecStart= est rendue à part.
func directives(t *testing.T, unite string) (lignes []string, execStart string) {
	t.Helper()
	s := bufio.NewScanner(strings.NewReader(unite))
	for s.Scan() {
		ligne := strings.TrimSpace(s.Text())
		switch {
		case ligne == "", strings.HasPrefix(ligne, "#"), strings.HasPrefix(ligne, ";"):
		case strings.HasPrefix(ligne, "ExecStart="):
			execStart = ligne
		default:
			lignes = append(lignes, ligne)
		}
	}
	if err := s.Err(); err != nil {
		t.Fatal(err)
	}
	return lignes, execStart
}

// uniteDocumentee rend le bloc ini de « L'unité systemd » dans INSTALL.md :
// c'est l'unité que l'hébergeant recopie à la main.
func uniteDocumentee(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile("../../INSTALL.md")
	if err != nil {
		t.Fatal(err)
	}
	_, apres, ok := strings.Cut(string(doc), "\n### L'unité systemd\n")
	if !ok {
		t.Fatal("INSTALL.md n'a plus de section « L'unité systemd »")
	}
	_, bloc, ok := strings.Cut(apres, "```ini\n")
	if !ok {
		t.Fatal("la section « L'unité systemd » n'a plus de bloc ini")
	}
	bloc, _, ok = strings.Cut(bloc, "```")
	if !ok {
		t.Fatal("le bloc ini de « L'unité systemd » n'est pas fermé")
	}
	return bloc
}

func uniteDuPaquet(t *testing.T) string {
	t.Helper()
	unite, err := os.ReadFile("../../paquets/patachoo.service")
	if err != nil {
		t.Fatal(err)
	}
	return string(unite)
}

func TestLUniteDuPaquetEstCelleDINSTALL(t *testing.T) {
	documentees, _ := directives(t, uniteDocumentee(t))
	empaquetees, _ := directives(t, uniteDuPaquet(t))

	if len(documentees) == 0 {
		t.Fatal("aucune directive lue dans INSTALL.md")
	}
	if !slices.Equal(documentees, empaquetees) {
		t.Errorf("les deux unités divergent hors ExecStart=\n--- INSTALL.md ---\n%s\n--- paquets/patachoo.service ---\n%s",
			strings.Join(documentees, "\n"), strings.Join(empaquetees, "\n"))
	}
}

func TestLePaquetLanceLeBinaireQuIlPoseDansUsrBin(t *testing.T) {
	_, documentee := directives(t, uniteDocumentee(t))
	_, empaquetee := directives(t, uniteDuPaquet(t))

	attendue := strings.Replace(documentee, "=/usr/local/bin/patachoo ", "=/usr/bin/patachoo ", 1)
	if attendue == documentee {
		t.Fatalf("l'ExecStart d'INSTALL.md ne lance plus /usr/local/bin/patachoo : %s", documentee)
	}
	if empaquetee != attendue {
		t.Errorf("ExecStart du paquet :\n  %s\nattendu :\n  %s", empaquetee, attendue)
	}
}

// maintenance joue paquets/<script> par /bin/sh avec les arguments que dpkg ou
// rpm lui passeraient. Le PATH ne contient que `outils` : un systemctl factice
// s'il y est posé, rien sinon. Elle rend le code de sortie, les sorties, et les
// appels reçus par le systemctl factice, un par ligne.
func maintenance(t *testing.T, outils, script string, args ...string) (code int, sorties string, appels []string) {
	t.Helper()
	chemin, err := filepath.Abs(filepath.Join("../../paquets", script))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", append([]string{chemin}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + outils}
	var tampon bytes.Buffer
	cmd.Stdout, cmd.Stderr = &tampon, &tampon
	err = cmd.Run()
	var sortieEnErreur *exec.ExitError
	if errors.As(err, &sortieEnErreur) {
		code = sortieEnErreur.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	journal, err := os.ReadFile(filepath.Join(outils, "appels"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(string(journal)), "\n") {
		if l != "" {
			appels = append(appels, l)
		}
	}
	return code, tampon.String(), appels
}

// systemctlFactice pose dans un répertoire neuf un systemctl qui note ses
// arguments et sort avec `code`.
func systemctlFactice(t *testing.T, code string) string {
	t.Helper()
	outils := t.TempDir()
	// Le PATH du script ne contient qu'outils : le factice n'a que des
	// commandes internes du shell à sa disposition.
	factice := "#!/bin/sh\necho \"$*\" >> \"${0%/*}/appels\"\nexit " + code + "\n"
	if err := os.WriteFile(filepath.Join(outils, "systemctl"), []byte(factice), 0o755); err != nil {
		t.Fatal(err)
	}
	return outils
}

// Les arguments de dpkg (configure, remove, purge, upgrade…) et ceux de rpm
// (le nombre d'exemplaires qui resteront installés) disent la même chose
// autrement : chaque cas les joue tous deux.
var casDeMaintenance = []struct {
	nom    string
	script string
	args   []string
	appels []string
}{
	{"deb, installation", "apres-installation.sh", []string{"configure"}, []string{"daemon-reload"}},
	{"deb, installation (version vide)", "apres-installation.sh", []string{"configure", ""}, []string{"daemon-reload"}},
	{"rpm, installation", "apres-installation.sh", []string{"1"}, []string{"daemon-reload"}},
	{"deb, mise à jour", "apres-installation.sh", []string{"configure", "0.2.0"},
		[]string{"daemon-reload", "try-restart patachoo.service"}},
	{"rpm, mise à jour", "apres-installation.sh", []string{"2"},
		[]string{"daemon-reload", "try-restart patachoo.service"}},

	{"deb, avant désinstallation", "avant-desinstallation.sh", []string{"remove"},
		[]string{"disable --now patachoo.service"}},
	{"rpm, avant désinstallation", "avant-desinstallation.sh", []string{"0"},
		[]string{"disable --now patachoo.service"}},
	{"deb, avant mise à jour", "avant-desinstallation.sh", []string{"upgrade", "0.3.0"}, nil},
	{"rpm, avant mise à jour", "avant-desinstallation.sh", []string{"1"}, nil},

	{"deb, après désinstallation", "apres-desinstallation.sh", []string{"remove"}, []string{"daemon-reload"}},
	{"deb, après purge", "apres-desinstallation.sh", []string{"purge"}, []string{"daemon-reload"}},
	{"rpm, après désinstallation", "apres-desinstallation.sh", []string{"0"}, []string{"daemon-reload"}},
}

func TestLesScriptsDeMaintenancePilotentSystemd(t *testing.T) {
	for _, c := range casDeMaintenance {
		t.Run(c.nom, func(t *testing.T) {
			code, sorties, appels := maintenance(t, systemctlFactice(t, "0"), c.script, c.args...)

			if code != 0 {
				t.Errorf("%s %q sort en %d :\n%s", c.script, c.args, code, sorties)
			}
			if !slices.Equal(appels, c.appels) {
				t.Errorf("%s %q appelle systemctl %q, attendu %q", c.script, c.args, appels, c.appels)
			}
		})
	}
}

// Un conteneur, un chroot : pas de systemctl du tout. Le paquet s'installe et
// se désinstalle quand même.
func TestSansSystemctlLesScriptsNeFontRienEtReussissent(t *testing.T) {
	for _, c := range casDeMaintenance {
		t.Run(c.nom, func(t *testing.T) {
			code, sorties, _ := maintenance(t, t.TempDir(), c.script, c.args...)

			if code != 0 || sorties != "" {
				t.Errorf("%s %q sans systemctl : code %d, sorties :\n%s", c.script, c.args, code, sorties)
			}
		})
	}
}

// Un systemctl présent qui échoue — systemd installé mais pas démarré, comme
// dans un chroot — ne fait pas échouer la transaction du gestionnaire de
// paquets : un paquet à moitié configuré coûterait plus cher qu'un service
// qu'on relance à la main.
func TestUnSystemctlEnEchecNeFaitPasEchouerLeScript(t *testing.T) {
	for _, c := range casDeMaintenance {
		t.Run(c.nom, func(t *testing.T) {
			code, sorties, _ := maintenance(t, systemctlFactice(t, "1"), c.script, c.args...)

			if code != 0 {
				t.Errorf("%s %q sort en %d quand systemctl échoue :\n%s", c.script, c.args, code, sorties)
			}
		})
	}
}
