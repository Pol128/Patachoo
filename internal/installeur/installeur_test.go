// Package installeur n'a aucun code de production : il n'existe que pour
// éprouver installer.sh, le script d'installation publié avec chaque Release.
// Il est donc lancé par `go test ./...`, et par ./verifie, sans rien demander
// de plus à l'environnement qu'un /bin/sh, curl et sha256sum.
//
// Le script est joué pour de vrai, par /bin/sh — c'est dash sur une Debian, ce
// qui fait déjà rougir la plupart des bashismes. Deux choses seulement sont
// substituées, et aucune par une dérogation ajoutée au script :
//
//   - les téléchargements vont vers un httptest.Server, par PATACHOO_URL_BASE,
//     le point d'entrée que le script offre à qui veut un miroir. La somme y
//     est vérifiée comme partout ailleurs ;
//   - la plateforme est choisie par un `uname` factice placé en tête du PATH.
//     C'est la détection telle qu'elle tournera chez l'hébergeant qu'on éprouve.
//
// Aucun test d'ici ne sort de la boucle locale : ni GitHub, ni réseau. Ils
// passent donc avec SANS_RESEAU=1.
//
// Chaque cas d'échec regarde le préfixe, pas seulement le code de sortie : un
// script qui poserait le binaire puis sortirait en erreur passerait sur le code
// seul.
package installeur

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// La version que la fausse release annonce, et le tag qui la désigne.
const (
	versionPubliee = "1.2.3"
	tagPublie      = "v1.2.3"
)

// fauxBinaire est un « patachoo » qui ne fait qu'une chose : dire sa version,
// comme `patachoo version`.
func fauxBinaire(version string) []byte {
	return []byte("#!/bin/sh\necho " + version + "\n")
}

// archive rend un tar.gz qui porte `patachoo` à sa racine, comme ceux de
// publier.yml.
func archive(t *testing.T, binaire []byte) []byte {
	t.Helper()
	var tampon bytes.Buffer
	gz := gzip.NewWriter(&tampon)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "patachoo", Mode: 0o755, Size: int64(len(binaire))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(binaire); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return tampon.Bytes()
}

func somme(contenu []byte) string {
	s := sha256.Sum256(contenu)
	return hex.EncodeToString(s[:])
}

// serveur sert des fichiers par chemin, et note tout ce qu'on lui demande.
type serveur struct {
	*httptest.Server
	mu       sync.Mutex
	demandes []string
}

func (s *serveur) chemins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.demandes...)
}

func servir(t *testing.T, fichiers map[string][]byte) *serveur {
	t.Helper()
	s := &serveur{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.demandes = append(s.demandes, r.URL.Path)
		s.mu.Unlock()
		contenu, ok := fichiers[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(contenu)
	}))
	t.Cleanup(s.Close)
	return s
}

// release sert, sous `racine`, l'archive amd64 d'un binaire qui annonce
// versionPubliee, et le fichier de sommes qui va avec — sauf si sommeAnnoncee
// est donnée, auquel cas c'est elle que le fichier annonce.
func release(t *testing.T, racine, sommeAnnoncee string) *serveur {
	t.Helper()
	tgz := archive(t, fauxBinaire(versionPubliee))
	if sommeAnnoncee == "" {
		sommeAnnoncee = somme(tgz)
	}
	sommes := fmt.Sprintf("%s  patachoo_linux_arm64.tar.gz\n%s  patachoo_linux_amd64.tar.gz\n",
		strings.Repeat("0", 64), sommeAnnoncee)
	return servir(t, map[string][]byte{
		racine + "/patachoo_linux_amd64.tar.gz": tgz,
		racine + "/sommes-sha256.txt":           []byte(sommes),
	})
}

// execution est ce qu'un lancement du script a rendu.
type execution struct {
	code           int
	sortie, erreur string
	outils         string // le répertoire du uname et du sudo factices
}

// lancer joue installer.sh par /bin/sh, sous la plateforme `systeme`/`archi`
// (ce que rendront `uname -s` et `uname -m`), avec les téléchargements dirigés
// vers `base`.
func lancer(t *testing.T, systeme, archi, base string, args ...string) execution {
	t.Helper()
	script, err := filepath.Abs("../../installer.sh")
	if err != nil {
		t.Fatal(err)
	}

	// Le sudo factice laisse une trace s'il est appelé : le script ne doit
	// jamais s'élever de lui-même.
	outils := t.TempDir()
	uname := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n-s) echo %s ;;\n-m) echo %s ;;\n*) echo %s ;;\nesac\n",
		systeme, archi, systeme)
	sudo := "#!/bin/sh\ntouch \"$(dirname \"$0\")/sudo-appele\"\n"
	for nom, contenu := range map[string]string{"uname": uname, "sudo": sudo} {
		if err := os.WriteFile(filepath.Join(outils, nom), []byte(contenu), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := exec.Command("/bin/sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+outils+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PATACHOO_URL_BASE="+base,
		"NO_PROXY=127.0.0.1", "no_proxy=127.0.0.1",
	)
	var sortie, erreur bytes.Buffer
	cmd.Stdout, cmd.Stderr = &sortie, &erreur
	err = cmd.Run()

	code := 0
	var sortieEnErreur *exec.ExitError
	if errors.As(err, &sortieEnErreur) {
		code = sortieEnErreur.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return execution{code, sortie.String(), erreur.String(), outils}
}

func (e execution) String() string {
	return fmt.Sprintf("code %d\n--- sortie ---\n%s--- erreur ---\n%s", e.code, e.sortie, e.erreur)
}

// prefixeVide échoue si le préfixe porte quoi que ce soit, fichier temporaire
// compris.
func prefixeVide(t *testing.T, prefixe string) {
	t.Helper()
	entrees, err := os.ReadDir(prefixe)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entrees {
		t.Errorf("le préfixe devait rester vide, il porte %s", e.Name())
	}
}

func TestInstallationNominalePoseLeBinaireEtImprimeSaVersion(t *testing.T) {
	s := release(t, "/latest/download", "")
	prefixe := t.TempDir()

	e := lancer(t, "Linux", "x86_64", s.URL, "--prefix", prefixe)

	if e.code != 0 {
		t.Fatalf("installation refusée :\n%s", e)
	}
	info, err := os.Stat(filepath.Join(prefixe, "patachoo"))
	if err != nil {
		t.Fatalf("aucun binaire posé : %v\n%s", err, e)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("binaire posé non exécutable : %v", info.Mode())
	}
	if !strings.Contains(e.sortie, versionPubliee) {
		t.Errorf("la version posée n'est pas imprimée sur la sortie standard :\n%s", e)
	}
}

func TestUneSommeQuiNeCorrespondPasNePoseRien(t *testing.T) {
	s := release(t, "/latest/download", somme([]byte("une autre archive")))
	prefixe := t.TempDir()

	e := lancer(t, "Linux", "x86_64", s.URL, "--prefix", prefixe)

	if e.code == 0 {
		t.Errorf("une somme fausse est acceptée :\n%s", e)
	}
	if !strings.Contains(strings.ToLower(e.erreur), "somme") {
		t.Errorf("la sortie d'erreur ne parle pas de la somme :\n%s", e)
	}
	prefixeVide(t, prefixe)
}

func TestUnBinaireDejaPresentNEstPasEcraseSansForce(t *testing.T) {
	s := release(t, "/latest/download", "")
	prefixe := t.TempDir()
	present := fauxBinaire("0.9.0")
	cible := filepath.Join(prefixe, "patachoo")
	if err := os.WriteFile(cible, present, 0o755); err != nil {
		t.Fatal(err)
	}

	e := lancer(t, "Linux", "x86_64", s.URL, "--prefix", prefixe)

	if e.code == 0 {
		t.Errorf("le script dit avoir réussi sans rien installer :\n%s", e)
	}
	tout := e.sortie + e.erreur
	if !strings.Contains(tout, "0.9.0") || !strings.Contains(tout, versionPubliee) {
		t.Errorf("les deux versions, présente et visée, ne sont pas annoncées :\n%s", e)
	}
	apres, err := os.ReadFile(cible)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(apres, present) {
		t.Errorf("le binaire en place a changé :\n%s", apres)
	}
	entrees, _ := os.ReadDir(prefixe)
	if len(entrees) != 1 {
		t.Errorf("le préfixe porte %d entrées, attendu le seul binaire en place", len(entrees))
	}
}

func TestForceRemplaceLeBinairePresent(t *testing.T) {
	s := release(t, "/latest/download", "")
	prefixe := t.TempDir()
	cible := filepath.Join(prefixe, "patachoo")
	if err := os.WriteFile(cible, fauxBinaire("0.9.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	e := lancer(t, "Linux", "x86_64", s.URL, "--prefix", prefixe, "--force")

	if e.code != 0 {
		t.Fatalf("--force refusé :\n%s", e)
	}
	apres, err := os.ReadFile(cible)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(apres, fauxBinaire(versionPubliee)) {
		t.Errorf("le binaire n'a pas été remplacé :\n%s", apres)
	}
}

func TestUnPrefixeNonAccessibleEnEcritureEstNommeSansSElever(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("en root, tout répertoire est accessible en écriture")
	}
	s := release(t, "/latest/download", "")
	prefixe := t.TempDir()
	if err := os.Chmod(prefixe, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(prefixe, 0o700) })

	e := lancer(t, "Linux", "x86_64", s.URL, "--prefix", prefixe)

	if e.code == 0 {
		t.Errorf("le script dit avoir réussi dans un préfixe en lecture seule :\n%s", e)
	}
	if !strings.Contains(e.erreur, prefixe) {
		t.Errorf("la sortie d'erreur ne nomme pas le préfixe %s :\n%s", prefixe, e)
	}
	if _, err := os.Stat(filepath.Join(e.outils, "sudo-appele")); err == nil {
		t.Errorf("le script a appelé sudo de lui-même")
	}
	prefixeVide(t, prefixe)
}

func TestUnePlateformeNonDistribueeRenvoieALaConstruction(t *testing.T) {
	for _, c := range []struct {
		nom, systeme, archi string
		construction        string
	}{
		{"Darwin", "Darwin", "arm64", "GOOS=darwin GOARCH=arm64 go build"},
		{"armv6", "Linux", "armv6l", "GOARM=6 GOARCH=arm go build"},
	} {
		t.Run(c.nom, func(t *testing.T) {
			s := release(t, "/latest/download", "")
			prefixe := t.TempDir()

			e := lancer(t, c.systeme, c.archi, s.URL, "--prefix", prefixe)

			if e.code != 0 {
				t.Errorf("une plateforme non distribuée est un renvoi, pas un échec :\n%s", e)
			}
			tout := e.sortie + e.erreur
			nommee := c.systeme
			if c.systeme == "Linux" {
				nommee = c.archi
			}
			if !strings.Contains(tout, nommee) {
				t.Errorf("le message ne nomme pas %s :\n%s", nommee, e)
			}
			if !strings.Contains(tout, c.construction) {
				t.Errorf("le message ne donne pas la ligne %q :\n%s", c.construction, e)
			}
			if d := s.chemins(); len(d) != 0 {
				t.Errorf("téléchargement tenté : %v", d)
			}
			prefixeVide(t, prefixe)
		})
	}
}

func TestUneVersionEpingleeDemandeLesActifsDeCetteVersion(t *testing.T) {
	s := release(t, "/download/"+tagPublie, "")
	prefixe := t.TempDir()

	e := lancer(t, "Linux", "x86_64", s.URL, "--prefix", prefixe, "--version", tagPublie)

	if e.code != 0 {
		t.Fatalf("installation de %s refusée :\n%s", tagPublie, e)
	}
	demandes := s.chemins()
	if len(demandes) == 0 {
		t.Fatal("aucun téléchargement")
	}
	for _, d := range demandes {
		if !strings.HasPrefix(d, "/download/"+tagPublie+"/") {
			t.Errorf("chemin demandé hors de la version épinglée : %s", d)
		}
	}
}
