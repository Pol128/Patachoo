package recuperation

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"
)

// Le nombre d'appels de la mesure, et l'écart de descripteurs qu'on tolère
// entre le début et la fin. Sans fermeture des connexions inactives, l'écart
// vaut environ 2 × appels — une socket cliente et son acceptée côté serveur par
// appel —, donc la marge ne laisse aucune ambiguïté.
const (
	appelsDeMesure   = 20
	ecartDescripteur = 5
)

// descripteursOuverts compte les descripteurs du processus. Linux seulement :
// l'appelant s'est déjà assuré que /proc existe.
func descripteursOuverts(t *testing.T) int {
	t.Helper()
	entrees, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("lecture de /proc/self/fd : %v", err)
	}
	return len(entrees)
}

// descripteursStabilises reprend la mesure jusqu'à ce qu'elle passe sous le
// plafond, ou jusqu'à expiration de l'attente. La fermeture côté serveur est
// asynchrone : une mesure prise une seule fois rendrait le test instable sur
// une machine chargée.
func descripteursStabilises(t *testing.T, plafond int) int {
	t.Helper()
	limite := time.Now().Add(time.Second)
	for {
		n := descripteursOuverts(t)
		if n <= plafond || time.Now().After(limite) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRecupereNeRetientPasDeConnexion : le transport d'un appel n'est plus
// joignable après le retour de Recupere, et il garde ses connexions inactives
// quatre-vingt-dix secondes. Vingt appels suffisent alors à retenir quarante
// sockets pour un travail qui n'en demande qu'une — c'est le chemin par lequel
// une fournée d'import épuise les descripteurs du serveur (PATA-69).
func TestRecupereNeRetientPasDeConnexion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("la mesure lit /proc/self/fd, qui n'existe que sous Linux")
	}

	s := serveur(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "<html><head><title>page</title></head><body>corps</body></html>")
	})

	// Un appel d'amorçage : il paie ce qui ne s'alloue qu'une fois, pour que
	// la mesure ne compte que les connexions retenues.
	if _, err := recupere(t, s.URL+"/amorce", autorise(s)); err != nil {
		t.Fatalf("appel d'amorçage : %v", err)
	}
	avant := descripteursOuverts(t)

	for i := range appelsDeMesure {
		if _, err := recupere(t, fmt.Sprintf("%s/page-%d", s.URL, i), autorise(s)); err != nil {
			t.Fatalf("appel %d : %v", i, err)
		}
	}

	apres := descripteursStabilises(t, avant+ecartDescripteur)
	if ecart := apres - avant; ecart > ecartDescripteur {
		t.Errorf("%d descripteurs ouverts avant, %d après %d appels : écart de %d, plafond %d — chaque appel retient sa connexion sortante",
			avant, apres, appelsDeMesure, ecart, ecartDescripteur)
	}
}
