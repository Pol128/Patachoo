package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// La sonde de santé du conteneur.
//
// L'image Docker est un scratch : elle ne contient que le binaire, donc ni
// curl, ni wget, ni /bin/sh. Le HEALTHCHECK ne peut s'appuyer que sur le
// binaire lui-même — d'où cette sous-commande, qui interroge l'application
// comme le ferait un client extérieur, par sa route publique de santé.

const (
	// adresseSanteDefaut est celle que sert le CMD de l'image. La sonde
	// s'exécute dans le conteneur : elle s'adresse à la boucle locale, jamais
	// au réseau. Elle ne vaut pas 0.0.0.0:8090 pour autant — c'est une adresse
	// d'écoute, pas une adresse à laquelle on se connecte.
	adresseSanteDefaut = "127.0.0.1:8090"

	// delaiSanteDefaut borne l'échange entier. Sans borne, une application qui
	// accepte la connexion sans jamais répondre laisserait la sonde pendre, et
	// Docker garderait le conteneur en « starting » au lieu de le déclarer
	// « unhealthy ». Trois secondes : c'est un GET sur la boucle locale, pas
	// une requête réseau.
	delaiSanteDefaut = 3 * time.Second
)

// commandeSante construit la sous-commande `patachoo healthcheck`.
//
// Elle sort par os.Exit et non par une erreur rendue : PocketBase lance
// RootCmd.Execute() dans une goroutine et jette son erreur — « leave to the
// commands to decide whether to print their error ». Une commande qui se
// contenterait de rendre une erreur sortirait donc avec le code 0, et le
// HEALTHCHECK dirait « sain » quoi qu'il arrive.
func commandeSante() *cobra.Command {
	var adresse string
	var delai time.Duration

	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Interroge /api/health et sort 0 si l'application répond",
		Long: "Sonde de santé du conteneur : un GET sur /api/health de l'adresse\n" +
			"donnée, code de sortie 0 si l'application répond 200, non nul sinon.\n" +
			"C'est ce que lance le HEALTHCHECK de l'image Docker, qui n'a pas de\n" +
			"shell pour appeler curl.",
		Run: func(cmd *cobra.Command, _ []string) {
			os.Exit(codeSante(adresse, delai, cmd.ErrOrStderr()))
		},
	}

	cmd.Flags().StringVar(&adresse, "http", adresseSanteDefaut,
		"adresse hôte:port à interroger")
	cmd.Flags().DurationVar(&delai, "delai", delaiSanteDefaut,
		"délai au-delà duquel la sonde renonce")

	return cmd
}

// codeSante rend le code de sortie du processus : 0 quand l'application répond
// 200 sur /api/health, 1 dans tous les autres cas — avec le motif écrit sur
// journal, seule trace que `docker inspect` montrera de l'échec.
func codeSante(adresse string, delai time.Duration, journal io.Writer) int {
	client := &http.Client{
		Timeout: delai,
		// Proxy à nil, et non celui de l'environnement : la sonde s'adresse à
		// la boucle locale de ce conteneur-ci. Un HTTP_PROXY posé dans
		// l'environnement enverrait le GET ailleurs, et la santé rapportée ne
		// serait plus celle de ce processus.
		Transport: &http.Transport{Proxy: nil},
	}

	cible := "http://" + adresse + "/api/health"

	reponse, err := client.Get(cible)
	if err != nil {
		fmt.Fprintf(journal, "sonde %s : %v\n", cible, err)
		return 1
	}
	defer reponse.Body.Close()

	if reponse.StatusCode != http.StatusOK {
		fmt.Fprintf(journal, "sonde %s : code %d, attendu 200\n", cible, reponse.StatusCode)
		return 1
	}

	return 0
}
