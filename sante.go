package main

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

// La sonde de santé du conteneur.
//
// L'image Docker est un scratch : elle ne contient que le binaire, donc ni
// curl, ni wget, ni /bin/sh. Le HEALTHCHECK ne peut s'appuyer que sur le
// binaire lui-même — d'où cette sous-commande, qui interroge l'application
// comme le ferait un client extérieur, par sa route publique de santé.
//
// Elle vit délibérément hors de app.RootCmd, et main() la traite avant tout le
// reste : une commande connue de PocketBase est amorcée par app.Start() avant
// d'être lancée — data.db et auxiliary.db ouvertes, migrations système jouées,
// puis pb_data/.pb_temp_to_delete effacé, alors que ce répertoire porte
// l'archive d'une sauvegarde ou l'extraction d'une restauration en cours. Un
// GET sur la boucle locale toutes les trente secondes n'a besoin de rien de
// tout cela, et n'a surtout pas à écrire dans le volume qu'il surveille.

// nomCommandeSante est le nom qu'appelle le HEALTHCHECK de l'image. Le changer
// ici sans le changer dans le Dockerfile rendrait le conteneur « unhealthy »
// alors qu'il répond : c'est TestSanteNeTouchePasAuRepertoireDeDonnees, qui
// lance le binaire par ce nom-là, qui tient la couture entre les deux.
const nomCommandeSante = "healthcheck"

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

// santeDemandee dit si les arguments du processus désignent la sonde. C'est le
// seul aiguillage : tout le reste part chez PocketBase, comme avant.
func santeDemandee(args []string) bool {
	return len(args) > 0 && args[0] == nomCommandeSante
}

// lanceSante exécute la sonde sur les arguments du processus — le nom de la
// commande compris, comme os.Args[1:] les donne — et rend le code de sortie.
func lanceSante(args []string, journal io.Writer) int {
	// 1 par défaut : une erreur d'analyse des drapeaux ne doit pas laisser
	// passer un « sain » qui n'a jamais été mesuré.
	code := 1

	cmd := commandeSante(&code)
	cmd.SetArgs(args[1:])
	cmd.SetOut(journal)
	cmd.SetErr(journal)

	if err := cmd.Execute(); err != nil {
		return 1
	}
	return code
}

// commandeSante construit la sous-commande `patachoo healthcheck`. Elle écrit
// le code de sortie dans code plutôt que de le rendre : cobra ne rapporte que
// des erreurs, et un échec de sonde n'en est pas une.
func commandeSante(code *int) *cobra.Command {
	var adresse string
	var delai time.Duration

	cmd := &cobra.Command{
		Use:   nomCommandeSante,
		Short: "Interroge /api/health et sort 0 si l'application répond",
		Long: "Sonde de santé du conteneur : un GET sur /api/health de l'adresse\n" +
			"donnée, code de sortie 0 si l'application répond 200, non nul sinon.\n" +
			"C'est ce que lance le HEALTHCHECK de l'image Docker, qui n'a pas de\n" +
			"shell pour appeler curl.",
		Run: func(cmd *cobra.Command, _ []string) {
			*code = codeSante(adresse, delai, cmd.ErrOrStderr())
		},
	}

	cmd.Flags().StringVar(&adresse, "http", adresseSanteDefaut,
		"adresse hôte:port à interroger")
	cmd.Flags().DurationVar(&delai, "delai", delaiSanteDefaut,
		"délai au-delà duquel la sonde renonce")

	// --dir est celui de PocketBase, et la sonde ne s'en sert pas : elle fait
	// un GET, elle n'ouvre pas la base. Il est accepté pour qu'une habitude —
	// `patachoo healthcheck --dir=/pb_data` — ne devienne pas une erreur, et
	// déclaré plutôt que toléré en masse pour qu'un drapeau mal orthographié
	// reste, lui, une erreur.
	cmd.Flags().String("dir", "",
		"accepté et ignoré : la sonde ne lit pas le répertoire de données")

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
