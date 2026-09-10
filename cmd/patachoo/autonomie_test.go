package main

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// La promesse de Patachoo tient en une phrase : un seul fichier à copier. Ce
// test la vérifie de bout en bout — compiler, poser le binaire seul dans un
// répertoire vide hors du dépôt, le lancer sans lui dire où trouver quoi que
// ce soit, et obtenir une instance qui répond.
//
// C'est le seul endroit où un fichier oublié dans un go:embed se voit : tous
// les autres tests tournent depuis le dépôt, où les gabarits et les assets
// sont sur le disque à côté d'eux, donc lisibles même s'ils n'étaient pas
// embarqués.
//
// Aucun test d'ici ne sort de la boucle locale : la compilation se fait avec
// GOPROXY=off, sur le cache de modules que la compilation du paquet de test
// vient de remplir, et les requêtes visent 127.0.0.1. Il passe donc avec
// SANS_RESEAU=1.

// delaiDemarrage borne l'attente du serveur. Le démarrage prend moins d'une
// seconde sur le poste de travail ; la marge couvre une machine de CI chargée,
// pas un blocage. Un test qui pend est pire qu'un test rouge : au delà de ce
// délai, on échoue en recrachant la sortie capturée du serveur.
const delaiDemarrage = 20 * time.Second

// Les redirections ne sont pas suivies : un 302 suivi jusqu'à un 200 se lirait
// comme un 200 et masquerait une page déplacée ou disparue.
var clientLocal = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func TestLeBinaireSeulDansUnRepertoireVideSuffit(t *testing.T) {
	repertoire := t.TempDir()
	binaire := filepath.Join(repertoire, "patachoo")
	compileLeBinaire(t, binaire)

	hote, journal := lanceLeBinaire(t, repertoire, binaire)

	// /connexion et non / : la racine redirige vers /recettes, que la session
	// garde. La page de connexion est la seule qu'un binaire tout juste
	// installé rende en entier, donc la seule qui prouve ici que les gabarits
	// et les assets embarqués sont bien là.
	pages := map[string]string{}
	for _, chemin := range []string{"/connexion", "/_/", "/api/health"} {
		reponse, err := clientLocal.Get("http://" + hote + chemin)
		if err != nil {
			t.Fatalf("%s n'a pas répondu : %v\n%s", chemin, err, journal())
		}
		corps, err := io.ReadAll(reponse.Body)
		reponse.Body.Close()
		if err != nil {
			t.Fatalf("%s : lecture du corps : %v", chemin, err)
		}
		if reponse.StatusCode != http.StatusOK {
			t.Errorf("%s a répondu %d, attendu 200\n%s", chemin, reponse.StatusCode, journal())
		}
		pages[chemin] = string(corps)
	}

	// Un code 200 ne suffit pas : le registre de gabarits rend une chaîne vide
	// sans la moindre erreur quand un fichier manque à l'appel (voir la
	// convention documentée dans pages.go). Il faut donc regarder le corps —
	// un marqueur de la mise en page, un marqueur du contenu.
	//
	var manquants []string
	for _, marqueur := range []string{
		"<!doctype html>",
		`<html lang="fr">`,
		`<a href="/recettes">Recettes</a>`,
		`action="/connexion"`,
	} {
		if !strings.Contains(pages["/connexion"], marqueur) {
			manquants = append(manquants, marqueur)
		}
	}
	if manquants != nil {
		t.Errorf("la page de connexion ne porte pas %q : les gabarits embarqués n'ont pas été rendus\ncorps reçu :\n%s",
			manquants, pages["/connexion"])
	}

	// La racine reste une entrée valide du produit : elle mène à la liste, et
	// c'est ici qu'on le vérifie sur le binaire livré, redirection non suivie.
	racine, err := clientLocal.Get("http://" + hote + "/")
	if err != nil {
		t.Fatalf("/ n'a pas répondu : %v\n%s", err, journal())
	}
	racine.Body.Close()
	if racine.StatusCode != http.StatusFound || racine.Header.Get("Location") != "/recettes" {
		t.Errorf("/ a répondu %d vers %q, attendu 302 vers %q",
			racine.StatusCode, racine.Header.Get("Location"), "/recettes")
	}

	// Le binaire n'a rien réclamé à côté de lui, et n'y a rien déposé d'autre
	// que ses données : c'est ce qui fait qu'on peut le copier seul.
	entrees, err := os.ReadDir(repertoire)
	if err != nil {
		t.Fatalf("impossible de lire %s : %v", repertoire, err)
	}
	noms := make([]string, 0, len(entrees))
	for _, entree := range entrees {
		noms = append(noms, entree.Name())
	}
	slices.Sort(noms)
	if attendu := []string{"patachoo", "pb_data"}; !slices.Equal(noms, attendu) {
		t.Errorf("le répertoire contient %q, attendu %q : un fichier a été déposé ou réclamé à côté du binaire",
			noms, attendu)
	}
}

// compileLeBinaire produit le livrable, et non le binaire de test : c'est bien
// `go build` qu'on vérifie, puisque c'est lui qui décide de ce qui part dans
// l'exécutable qu'on distribue.
func compileLeBinaire(t *testing.T, vers string) {
	t.Helper()

	cmd := exec.Command("go", "build", "-o", vers, ".")
	// GOPROXY=off : la compilation ne touche que le cache de modules, déjà
	// rempli par celle du paquet de test. C'est ce qui tient la promesse
	// « aucune connexion hors de 127.0.0.1 ».
	cmd.Env = append(os.Environ(), "GOPROXY=off")

	if sortie, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build a échoué : %v\n%s", err, sortie)
	}
}

// lanceLeBinaire démarre le binaire depuis le répertoire où il est posé, avec
// pour seuls arguments serve et --http. Pas de --dir : savoir se débrouiller
// là où on l'a copié est précisément ce qui est en jeu.
//
// Elle rend l'hôte:port du serveur et un accès à sa sortie, que les messages
// d'échec recrachent — un test qui dit « pas de réponse » sans montrer ce que
// le serveur a écrit oblige à tout refaire à la main.
func lanceLeBinaire(t *testing.T, repertoire, binaire string) (string, func() string) {
	t.Helper()

	hote := "127.0.0.1:" + portLibre(t)
	journal := &tamponSur{}

	cmd := exec.Command(binaire, "serve", "--http="+hote)
	cmd.Dir = repertoire
	cmd.Stdout = journal
	cmd.Stderr = journal

	if err := cmd.Start(); err != nil {
		t.Fatalf("impossible de lancer %s : %v", binaire, err)
	}

	// Dans un t.Cleanup, et non un defer : le processus doit mourir même
	// lorsque le test échoue en cours de route. Sans quoi go test laisserait un
	// patachoo derrière lui, et le ménage de t.TempDir passerait sous ses pieds.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	attendLaSante(t, hote, journal.String)
	return hote, journal.String
}

// portLibre rend un port attribué par le système puis relâché aussitôt. Un
// port codé en dur ferait échouer deux exécutions concurrentes du test, et
// tomberait sur l'instance de développement de celui qui le lance.
func portLibre(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("impossible de réserver un port : %v", err)
	}
	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("adresse inattendue %q : %v", l.Addr(), err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("impossible de relâcher le port : %v", err)
	}
	return port
}

// attendLaSante attend que le serveur réponde, et abandonne au bout du délai.
func attendLaSante(t *testing.T, hote string, journal func() string) {
	t.Helper()

	limite := time.Now().Add(delaiDemarrage)
	for time.Now().Before(limite) {
		reponse, err := clientLocal.Get("http://" + hote + "/api/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, reponse.Body)
			reponse.Body.Close()
			if reponse.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("aucune réponse 200 sur /api/health au bout de %s\n%s", delaiDemarrage, journal())
}

// tamponSur recueille la sortie du serveur. Le verrou n'est pas décoratif : les
// goroutines de os/exec y écrivent pendant que le test le lit.
type tamponSur struct {
	mu     sync.Mutex
	tampon bytes.Buffer
}

func (t *tamponSur) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tampon.Write(p)
}

func (t *tamponSur) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.tampon.String()
}
