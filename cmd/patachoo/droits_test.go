//go:build unix

package main

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// Ce que `data.db` porte en clair — les secrets de signature des jetons des
// collections d'authentification, dans `_collections.options` — suffit à
// fabriquer un jeton superuser valable et à entrer dans `/_/`. Le fichier n'a
// donc rien à accorder aux autres comptes de la machine, et la seule chose qui
// le garantisse est le mode sous lequel le serveur l'écrit.
//
// Le test porte sur le binaire livré, et non sur le paquet : l'umask est un
// état de processus, que personne ne peut vérifier depuis un test in-process
// sans le changer pour tous les autres tests du paquet, qui tournent à côté.
//
// Et il le lance sous un umask volontairement permissif : sans cela il
// passerait par hasard sur une machine déjà stricte, et ne dirait plus rien du
// code. C'est le serveur qui doit resserrer, pas l'environnement qui l'héberge.
func TestPbDataNAccordeRienAuxAutresComptes(t *testing.T) {
	repertoire := t.TempDir()
	binaire := filepath.Join(repertoire, "patachoo")
	compileLeBinaire(t, binaire)

	lanceLeBinaireSousUmask(t, repertoire, binaire, "0000")

	// Tout ce qui est là après amorçage : le répertoire lui-même, les deux
	// bases et leurs fichiers d'accompagnement, et les sous-répertoires que
	// PocketBase crée au passage.
	pbData := filepath.Join(repertoire, "pb_data")
	var ouverts []string
	err := filepath.WalkDir(pbData, func(chemin string, entree fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		infos, err := entree.Info()
		if err != nil {
			return err
		}
		if mode := infos.Mode().Perm(); mode&0o077 != 0 {
			relatif, err := filepath.Rel(repertoire, chemin)
			if err != nil {
				relatif = chemin
			}
			ouverts = append(ouverts, fmt.Sprintf("%04o %s", mode, relatif))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("impossible de parcourir %s : %v", pbData, err)
	}

	if ouverts != nil {
		// Trié : la liste est lue par un humain, et l'ordre de parcours d'un
		// répertoire n'a pas à faire varier le message d'échec.
		sort.Strings(ouverts)
		t.Errorf("ces entrées de pb_data accordent un droit à group ou à other,"+
			" attendu 0700 pour les répertoires et 0600 pour les fichiers :\n%s",
			formateLesModes(ouverts))
	}
}

// formateLesModes met une entrée par ligne, indentée, pour que l'échec se lise
// sans avoir à relancer un stat à la main.
func formateLesModes(entrees []string) string {
	var rendu string
	for _, entree := range entrees {
		rendu += "  " + entree + "\n"
	}
	return rendu
}

// lanceLeBinaireSousUmask fait ce que lanceLeBinaire fait, à ceci près que le
// serveur hérite de l'umask demandé.
//
// Il passe par un shell intermédiaire, et non par un syscall.Umask dans le
// processus de test : l'umask est partagé par tout le processus, donc le poser
// ici le poserait aussi pour les autres tests du paquet. Le shell le pose pour
// lui-même, puis `exec` se remplace par le serveur, qui est alors le seul à
// l'avoir. $0 et $1 évitent d'avoir à échapper le chemin et l'hôte dans la
// commande.
func lanceLeBinaireSousUmask(t *testing.T, repertoire, binaire, umask string) (string, func() string) {
	t.Helper()

	hote := "127.0.0.1:" + portLibre(t)
	journal := &tamponSur{}

	cmd := exec.Command("/bin/sh", "-c",
		"umask "+umask+`; exec "$0" serve --http="$1"`, binaire, hote)
	cmd.Dir = repertoire
	cmd.Stdout = journal
	cmd.Stderr = journal

	if err := cmd.Start(); err != nil {
		t.Fatalf("impossible de lancer %s sous umask %s : %v", binaire, umask, err)
	}

	// Comme dans lanceLeBinaire : un Cleanup, pour que le serveur meure même
	// quand le test échoue en cours de route.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	attendLaSante(t, hote, journal.String)
	return hote, journal.String
}
