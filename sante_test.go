package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
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
