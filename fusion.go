package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// brancheLesCommandes ajoute nos sous-commandes à la racine du binaire et rend
// un témoin : interrogé après l'exécution, il dit si une commande a échoué.
//
// PocketBase amorce l'application avant d'exécuter la commande : une
// sous-commande dispose donc d'un core.App complet, base ouverte, sans rien
// monter de plus. En revanche, Execute() ignore délibérément l'erreur rendue
// par la racine cobra — « leave to the commands to decide whether to print
// their error » — et app.Start() ne rend donc rien à main(). Sans ce témoin,
// une fusion refusée s'arrêterait sur un code de retour nul, et le script qui
// l'appelle la croirait passée.
func brancheLesCommandes(app core.App, racine *cobra.Command) (aEchoue func() bool) {
	echec := false
	retient := func(err error) error {
		if err != nil {
			echec = true
		}
		return err
	}

	// La version de Patachoo, et non celle de PocketBase : la bibliothèque pose
	// « (untracked) » sur la racine, ce qui suffit à cobra pour ajouter
	// --version. La laisser en l'état livrerait deux réponses contradictoires à
	// la même question dans le même binaire.
	racine.Version = versionAffichee

	racine.AddCommand(commandeVersion())
	racine.AddCommand(commandeTags(app, retient))

	return func() bool { return echec }
}

func commandeTags(app core.App, retient func(error) error) *cobra.Command {
	tags := &cobra.Command{
		Use:   "tags",
		Short: "Entretien des tags.",
	}
	tags.AddCommand(commandeFusionner(app, retient))
	return tags
}

// commandeFusionner : une commande, pas un écran. Une page de fusion ouverte à
// tout compte connecté laisserait n'importe quel utilisateur réécrire les tags
// de tout le monde, et il n'y a aujourd'hui aucun modèle de rôles où poser
// cette autorisation. L'accès d'une commande est celui du shell de la machine.
func commandeFusionner(app core.App, retient func(error) error) *cobra.Command {
	var appliquer bool

	cmd := &cobra.Command{
		Use:   "fusionner <source> <cible>",
		Short: "Reporte les recettes du tag source sur le tag cible, puis supprime le tag source.",
		Long: "Les deux arguments sont des slugs : c'est ce qui est stable, sans accent\n" +
			"et sans casse, et c'est déjà la clé d'unicité des tags.\n\n" +
			"Sans --appliquer, la commande annonce ce qu'elle ferait et s'arrête.",
		Args: cobra.ExactArgs(2),
		// Une erreur d'exécution n'est pas une erreur d'usage : afficher
		// l'aide complète par-dessus le message noierait ce dernier.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return retient(rendCompteDeLaFusion(app, cmd.OutOrStdout(), args[0], args[1], appliquer))
		},
	}

	cmd.Flags().BoolVar(&appliquer, "appliquer", false,
		"écrire les modifications ; sans ce drapeau, rien n'est modifié")

	return cmd
}

// rendCompteDeLaFusion annonce l'ampleur de la fusion, puis l'applique si on
// le lui demande. Le compte rendu précède l'écriture : une opération
// irréversible qui s'exécute au premier essai est une opération qu'on regrette.
func rendCompteDeLaFusion(app core.App, sortie io.Writer, source, cible string, appliquer bool) error {
	_, _, recettes, err := fusionPrevue(app, source, cible)
	if err != nil {
		return err
	}

	fmt.Fprintf(sortie, "fusion du tag « %s » vers « %s » : %d recette(s) concernée(s).\n",
		source, cible, len(recettes))

	if !appliquer {
		fmt.Fprintln(sortie, "rien n'a été écrit — relancer avec --appliquer pour appliquer la fusion.")
		return nil
	}

	modifiees, err := fusionnerTags(app, source, cible)
	if err != nil {
		return err
	}
	fmt.Fprintf(sortie, "fusion appliquée : %d recette(s) modifiée(s), le tag « %s » a été supprimé.\n",
		modifiees, source)

	return nil
}

// fusionnerTags reporte les recettes du tag source sur le tag cible, supprime
// le tag source, et rend le nombre de recettes modifiées.
//
// Le tout dans une transaction : une recette qui refuse sa mise à jour laisse
// la base telle qu'elle était, tag source compris. Une fusion à moitié faite
// est pire que pas de fusion — elle ne se voit pas.
func fusionnerTags(app core.App, source, cible string) (int, error) {
	modifiees := 0

	err := app.RunInTransaction(func(txApp core.App) error {
		tagSource, tagCible, recettes, err := fusionPrevue(txApp, source, cible)
		if err != nil {
			return err
		}

		for _, recette := range recettes {
			recette.Set("tags", tagsApresFusion(recette.GetStringSlice("tags"), tagSource.Id, tagCible.Id))
			if err := txApp.Save(recette); err != nil {
				return fmt.Errorf("recette %q : %w", recette.GetString("title"), err)
			}
		}

		if err := txApp.Delete(tagSource); err != nil {
			return fmt.Errorf("suppression du tag %q : %w", source, err)
		}

		modifiees = len(recettes)
		return nil
	})
	if err != nil {
		return 0, err
	}

	return modifiees, nil
}

// fusionPrevue résout les deux slugs et rend les recettes qui portent la
// source. Elle n'écrit rien : c'est ce qui permet à la commande d'annoncer
// l'ampleur de la fusion avant de la faire.
func fusionPrevue(app core.App, source, cible string) (tagSource, tagCible *core.Record, recettes []*core.Record, err error) {
	if source == cible {
		// Fusionner un tag avec lui-même le retirerait des recettes puis le
		// supprimerait : une perte, pas une opération nulle.
		return nil, nil, nil, fmt.Errorf("fusion du tag %q vers lui-même : la source et la cible sont le même slug", source)
	}

	if tagSource, err = tagParSlug(app, source); err != nil {
		return nil, nil, nil, err
	}
	if tagCible, err = tagParSlug(app, cible); err != nil {
		return nil, nil, nil, err
	}

	// « tags.id » et non « tags » : sur un champ de relation, le nom seul se
	// résout en jointure vers la collection liée et ne se compare pas à un
	// identifiant — le filtre ne rendrait alors aucune recette, sans erreur.
	// Sur un champ multi-valué, « ?= » veut dire « au moins une valeur
	// correspond » ; « = » exigerait que toutes correspondent. Une limite
	// nulle ne borne pas le nombre de résultats.
	recettes, err = app.FindRecordsByFilter(
		"recipes", "tags.id ?= {:id}", "", 0, 0, dbx.Params{"id": tagSource.Id})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("recettes portant le tag %q : %w", source, err)
	}

	return tagSource, tagCible, recettes, nil
}

// tagParSlug rend le tag portant ce slug, ou une erreur qui nomme le slug
// fautif — c'est le seul renseignement utile à qui s'est trompé de frappe.
func tagParSlug(app core.App, slug string) (*core.Record, error) {
	// La valeur vient de la ligne de commande : elle passe par params, jamais
	// par concaténation dans le filtre.
	tag, err := app.FindFirstRecordByFilter("tags", "slug = {:slug}", dbx.Params{"slug": slug})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("tag %q : aucun tag ne porte ce slug", slug)
	case err != nil:
		return nil, fmt.Errorf("recherche du tag %q : %w", slug, err)
	}
	return tag, nil
}

// tagsApresFusion retire la source et ajoute la cible si elle n'y est pas
// déjà — sinon la recette ressortirait avec la cible en double. L'ordre des
// autres tags ne bouge pas : une fusion n'a pas à réordonner les fiches.
//
// PocketBase garantit déjà les deux, par ailleurs : un champ de relation
// déduplique ses valeurs à l'enregistrement, et supprimer un tag retire ses
// références des recettes. Le dire ici quand même laisse la recette écrite
// une seule fois, et dans l'état voulu — plutôt qu'écrite fausse puis
// rattrapée par un nettoyage dont l'ordre ne nous appartient pas.
func tagsApresFusion(tags []string, source, cible string) []string {
	apres := make([]string, 0, len(tags))
	porteLaCible := false

	for _, id := range tags {
		if id == source {
			continue
		}
		if id == cible {
			porteLaCible = true
		}
		apres = append(apres, id)
	}

	if !porteLaCible {
		apres = append(apres, cible)
	}

	return apres
}
