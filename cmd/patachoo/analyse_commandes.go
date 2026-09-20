package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// Les deux sous-commandes de l'établi : lancer une analyse, et lire ce qu'elle
// a donné.
//
// Des commandes, et pas des écrans — les écrans viennent ensuite
// (PATA-124 à PATA-127). C'est ce qui permet de dessiner ces écrans-là sur des
// chiffres réels plutôt que sur ceux de la forge : si les mots perdus sont 470
// et les non-résolus 40 000, les pages ne se dessinent pas de la même façon.
//
// Aucune règle d'accès à poser : l'accès d'une commande est celui du shell de
// la machine — c'est le raisonnement déjà écrit en tête de commandeFusionner.

// categorieAbsente est ce que le résumé affiche pour une forme que le lexique
// ne résout pas. Une ligne nommée plutôt qu'une ligne vide : c'est souvent le
// plus gros nombre du tableau, et il doit se lire.
const categorieAbsente = "sans catégorie"

func commandeAnalyse(app core.App, a *analyseur, retient func(error) error) *cobra.Command {
	analyse := &cobra.Command{
		Use:   "analyse",
		Short: "L'établi : analyser un corpus de lignes d'ingrédients.",
	}
	analyse.AddCommand(commandeAnalyseLancer(app, a, retient))
	analyse.AddCommand(commandeAnalyseResume(app, retient))
	return analyse
}

// commandeAnalyseLancer mène la passe sur la goroutine de la commande : il n'y
// a pas d'ouvrier qui tourne derrière un binaire lancé à la main, et la
// commande doit pouvoir dire ce que la passe a donné.
func commandeAnalyseLancer(app core.App, a *analyseur, retient func(error) error) *cobra.Command {
	return &cobra.Command{
		Use:   "lancer [fichier]",
		Short: "Analyse la base de l'instance, ou le fichier donné.",
		Long: "Sans argument, les lignes analysées sont celles de la collection\n" +
			"ingredients — lues, jamais écrites. Avec un argument, c'est un fichier du\n" +
			"disque de la machine, une ligne par ingrédient ; il est lu sur place et\n" +
			"n'est ni copié ni déplacé.\n\n" +
			"Une analyse à la fois : un second lancement est refusé tant que la\n" +
			"précédente n'a pas rendu la main.",
		Args: cobra.MaximumNArgs(1),
		// Une erreur d'exécution n'est pas une erreur d'usage : afficher
		// l'aide complète par-dessus le message noierait ce dernier.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return retient(lanceUneAnalyse(cmd.Context(), app, a, cmd.OutOrStdout(), args))
		},
	}
}

func lanceUneAnalyse(ctx context.Context, app core.App, a *analyseur, sortie io.Writer, args []string) error {
	source, lignes := sourceInstance, lignesDeLInstance(app)
	if len(args) == 1 {
		// Ouvert avant que la passe soit réservée : un chemin fautif ne doit
		// pas laisser une analyse vide derrière lui.
		fichier, err := os.Open(args[0])
		if err != nil {
			return fmt.Errorf("lecture du corpus : %w", err)
		}
		defer fichier.Close()

		source, lignes = sourceFournie, lignesDUnReader(fichier)
	}

	ouvrier := nouvelOuvrierDAnalyse(app, horlogeSysteme{}, a)
	passe, err := ouvrier.lance(ctx, source, lignes)
	if err != nil {
		return err
	}

	fmt.Fprintf(sortie, "analyse %s : %d ligne(s) lue(s), %d forme(s) distincte(s).\n",
		passe.Id, passe.GetInt("lines"), passe.GetInt("forms"))
	fmt.Fprintf(sortie, "le détail : patachoo analyse resume %s\n", passe.Id)
	return nil
}

func commandeAnalyseResume(app core.App, retient func(error) error) *cobra.Command {
	return &cobra.Command{
		Use:   "resume <id>",
		Short: "Rend le résumé d'une analyse : formes, signaux, résolution, catégories.",
		Long: "Une mesure par ligne, sur la sortie standard. C'est de quoi dessiner les\n" +
			"écrans de l'établi sur les chiffres de la base plutôt que sur ceux de la\n" +
			"forge.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return retient(rendLeResumeDeLAnalyse(app, cmd.OutOrStdout(), args[0]))
		},
	}
}

// rendLeResumeDeLAnalyse écrit les mesures d'une passe sur la sortie de la
// commande — et non dans le journal : c'est un résultat qu'on lit, qu'on
// redirige et qu'on compare, pas une trace d'exploitation.
func rendLeResumeDeLAnalyse(app core.App, sortie io.Writer, id string) error {
	passe, err := app.FindRecordById("analyses", id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("analyse %q : aucune analyse ne porte cet identifiant", id)
	case err != nil:
		return fmt.Errorf("recherche de l'analyse %q : %w", id, err)
	}

	mesures, err := resumeDe(app, passe)
	if err != nil {
		return err
	}

	fmt.Fprintf(sortie, "analyse %s — source %s, statut %s\n",
		passe.Id, passe.GetString("source"), passe.GetString("status"))
	fmt.Fprintf(sortie, "lignes : %d\n", passe.GetInt("lines"))
	fmt.Fprintf(sortie, "formes : %d\n", mesures.formes)
	fmt.Fprintf(sortie, "résolus : %d\n", mesures.resolus)
	fmt.Fprintf(sortie, "non résolus : %d\n", mesures.formes-mesures.resolus)
	for _, signal := range triees(mesures.parSignal) {
		fmt.Fprintf(sortie, "signal %s : %d\n", signal, mesures.parSignal[signal])
	}
	for _, categorie := range triees(mesures.parCategorie) {
		fmt.Fprintf(sortie, "catégorie %s : %d\n", categorie, mesures.parCategorie[categorie])
	}
	return nil
}

// mesuresDUneAnalyse porte ce que le résumé compte. Les comptes portent sur les
// formes et non sur les occurrences : ce qu'un relecteur regarde est une ligne
// distincte, qu'elle ait été vue une fois ou mille.
type mesuresDUneAnalyse struct {
	formes       int
	resolus      int
	parSignal    map[string]int
	parCategorie map[string]int
}

// resumeDe compte les formes d'une passe.
//
// En mémoire plutôt qu'en SQL : les signaux sont un tableau JSON, et les
// agréger en SQLite demanderait un json_each par ligne pour gagner, sur les
// cinquante mille formes d'un corpus entier, un temps qu'une commande lancée à
// la main ne remarquera pas.
func resumeDe(app core.App, passe *core.Record) (mesuresDUneAnalyse, error) {
	formes, err := app.FindAllRecords("analyses_formes", dbx.HashExp{"analysis": passe.Id})
	if err != nil {
		return mesuresDUneAnalyse{}, fmt.Errorf("lecture des formes de l'analyse %q : %w", passe.Id, err)
	}

	mesures := mesuresDUneAnalyse{
		formes:       len(formes),
		parSignal:    map[string]int{},
		parCategorie: map[string]int{},
	}
	for _, forme := range formes {
		if forme.GetBool("resolved") {
			mesures.resolus++
		}

		categorie := strings.TrimSpace(forme.GetString("category"))
		if categorie == "" {
			categorie = categorieAbsente
		}
		mesures.parCategorie[categorie]++

		var signaux []string
		if err := forme.UnmarshalJSONField("signals", &signaux); err != nil {
			return mesuresDUneAnalyse{}, fmt.Errorf("signaux de la forme %q : %w", forme.Id, err)
		}
		for _, signal := range signaux {
			mesures.parSignal[signal]++
		}
	}
	return mesures, nil
}

// triees rend les clés d'un compte, dans l'ordre : une sortie qu'on compare
// d'une passe à l'autre ne peut pas dépendre du parcours d'une table de
// hachage.
func triees(comptes map[string]int) []string {
	cles := make([]string, 0, len(comptes))
	for cle := range comptes {
		cles = append(cles, cle)
	}
	slices.Sort(cles)
	return cles
}
