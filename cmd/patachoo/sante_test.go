package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// La sonde de santé est ce sur quoi s'appuie le HEALTHCHECK de l'image Docker :
// l'image finale est un scratch, elle n'a ni curl ni wget, donc c'est le binaire
// qui s'interroge lui-même. Sa promesse tient en une phrase — code de sortie 0
// quand l'application répond 200 sur /api/health, code non nul dans tous les
// autres cas, et jamais d'attente non bornée.
//
// Aucun test d'ici ne sort de la boucle locale : les serveurs sont des httptest,
// et l'adresse « où personne n'écoute » est un port de 127.0.0.1 libéré juste
// avant. Ils passent donc avec SANS_RESEAU=1.

func TestSanteRendZeroQuandLApplicationRepond(t *testing.T) {
	var vu string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vu = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()

	var journal bytes.Buffer
	code := codeSante(adresse(t, s), delaiSanteDefaut, &journal)

	if code != 0 {
		t.Errorf("code de sortie %d, attendu 0 : %s", code, journal.String())
	}
	if vu != "/api/health" {
		t.Errorf("la sonde a interrogé %q, attendu /api/health", vu)
	}
}

func TestSanteRendNonZeroSurUnCodeAutreQue200(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "indisponible", http.StatusServiceUnavailable)
	}))
	defer s.Close()

	var journal bytes.Buffer
	if code := codeSante(adresse(t, s), delaiSanteDefaut, &journal); code == 0 {
		t.Error("code de sortie 0 alors que l'application a répondu 503")
	}
	if journal.Len() == 0 {
		t.Error("échec muet : docker inspect n'aurait aucun motif à montrer")
	}
}

func TestSanteRendNonZeroQuandPersonneNEcoute(t *testing.T) {
	var journal bytes.Buffer
	if code := codeSante(adresseMorte(t), delaiSanteDefaut, &journal); code == 0 {
		t.Error("code de sortie 0 alors que rien n'écoute sur l'adresse")
	}
	if journal.Len() == 0 {
		t.Error("échec muet : docker inspect n'aurait aucun motif à montrer")
	}
}

// Le délai est borné : une application qui accepte la connexion puis ne répond
// jamais ne doit pas laisser la sonde pendre. Sans borne, le HEALTHCHECK resterait
// en « starting » indéfiniment au lieu de basculer en « unhealthy ».
func TestSanteAbandonneQuandLApplicationNeRepondJamais(t *testing.T) {
	bloque := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-bloque
	}))
	defer s.Close()
	defer close(bloque)

	var journal bytes.Buffer
	debut := time.Now()
	code := codeSante(adresse(t, s), 50*time.Millisecond, &journal)
	ecoule := time.Since(debut)

	if code == 0 {
		t.Error("code de sortie 0 alors que l'application n'a jamais répondu")
	}
	if ecoule > 5*time.Second {
		t.Errorf("la sonde a attendu %s : le délai n'est pas borné", ecoule)
	}
}

// adresse rend l'hôte:port d'un serveur de test, sans son schéma : c'est ce que
// porte le drapeau --http, et c'est donc ce que la sonde reçoit.
func adresse(t *testing.T, s *httptest.Server) string {
	t.Helper()
	return s.Listener.Addr().String()
}

// adresseMorte rend une adresse de boucle locale où personne n'écoute : un port
// attribué par le système, puis relâché aussitôt.
func adresseMorte(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("impossible de réserver un port : %v", err)
	}
	a := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("impossible de relâcher le port : %v", err)
	}
	return a
}

// Le HEALTHCHECK de l'image lance `/patachoo healthcheck` toutes les trente
// secondes, sans --dir, donc sur /pb_data : le volume vivant, celui que sert le
// processus en cours. La sonde ne doit donc rien y écrire ni rien y supprimer —
// en particulier pas .pb_temp_to_delete, où PocketBase dépose l'archive d'une
// sauvegarde en cours et l'extraction d'une restauration en cours.
//
// Ce test exerce le binaire entier plutôt qu'une fonction : c'est main() qui
// décide si `healthcheck` passe par l'amorçage de l'application ou non, et
// aucun appel direct à la sonde ne verrait cette décision-là. Il couvre du même
// coup le nom de la sous-commande et ses drapeaux, qui sont la couture entre le
// Dockerfile et le code Go.
func TestSanteNeTouchePasAuRepertoireDeDonnees(t *testing.T) {
	sonde := make(chan string, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sonde <- r.URL.Path:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()

	donnees := t.TempDir()
	temoin := filepath.Join(donnees, ".pb_temp_to_delete", "temoin")
	if err := os.MkdirAll(filepath.Dir(temoin), 0o755); err != nil {
		t.Fatalf("impossible de préparer le répertoire de données : %v", err)
	}
	if err := os.WriteFile(temoin, []byte("sauvegarde en cours"), 0o644); err != nil {
		t.Fatalf("impossible de déposer le témoin : %v", err)
	}

	sortie, code := lancePatachoo(t, "healthcheck", "--dir="+donnees, "--http="+adresse(t, s))

	if code != 0 {
		t.Errorf("code de sortie %d alors que l'application a répondu 200 : %s", code, sortie)
	}
	// Sans cette vérification, un binaire qui ne connaîtrait pas la commande
	// passerait le test : PocketBase jette l'erreur d'une commande inconnue et
	// sort 0, donc « sain » sans avoir rien mesuré. C'est ce qui tient le nom
	// que le HEALTHCHECK du Dockerfile appelle.
	select {
	case vu := <-sonde:
		if vu != "/api/health" {
			t.Errorf("la sonde a interrogé %q, attendu /api/health", vu)
		}
	default:
		t.Errorf("le binaire n'a interrogé personne : la commande %q n'a pas été exécutée : %s",
			"healthcheck", sortie)
	}
	if _, err := os.Stat(temoin); err != nil {
		t.Errorf("la sonde a effacé le travail en cours dans .pb_temp_to_delete : %v", err)
	}
	if _, err := os.Stat(filepath.Join(donnees, "data.db")); err == nil {
		t.Error("la sonde a créé data.db : elle amorce l'application au lieu de faire un GET")
	}
}

// Un drapeau que la sonde ne connaît pas ne doit pas se solder par un « sain ».
// C'est le cas d'une erreur d'analyse : rien n'a été mesuré, donc rien ne
// permet de dire que l'application répond.
func TestSanteRendNonZeroSurUnDrapeauInconnu(t *testing.T) {
	sortie, code := lancePatachoo(t, "healthcheck", "--inconnu")

	if code == 0 {
		t.Errorf("code de sortie 0 sur un drapeau inconnu, sans qu'aucune sonde ait eu lieu : %s", sortie)
	}
}

// lancePatachoo relance le binaire de test avec PATACHOO_SOUS_PROCESSUS=1 :
// TestMain appelle alors main() au lieu de lancer les tests. C'est le seul
// moyen d'observer le chemin d'exécution réel d'une commande qui sort par
// os.Exit, code de sortie compris.
func lancePatachoo(t *testing.T, args ...string) (string, int) {
	t.Helper()

	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "PATACHOO_SOUS_PROCESSUS=1")

	sortie, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		t.Fatalf("le binaire n'a pas pu être lancé : %v", err)
	}
	return string(sortie), cmd.ProcessState.ExitCode()
}

// TestMain donne au binaire de test un second rôle : lancé avec
// PATACHOO_SOUS_PROCESSUS=1, il est le programme lui-même. Sans la variable,
// il se comporte en binaire de test ordinaire.
func TestMain(m *testing.M) {
	if os.Getenv("PATACHOO_SOUS_PROCESSUS") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}
