package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// La version du binaire.
//
// La source de vérité est un tag git annoté — v0.1.0 — et rien d'autre : pas de
// fichier VERSION à la racine, qui doublerait le tag et finirait par en
// diverger. Le tag arrive jusqu'ici par l'éditeur de liens, au build.
//
// debug.ReadBuildInfo() ne peut pas suppléer dans l'image : .git/ est exclu du
// contexte de construction (.dockerignore), il n'y a donc aucune estampille VCS
// à lire. C'est -ldflags ou rien. En build local, en revanche, l'estampille
// existe — et un binaire construit à la main doit dire ce qu'il est plutôt que
// de se taire.

// version est renseignée au build par -ldflags "-X main.version=0.1.0". Écrite
// en toutes lettres, et non par la constante ci-dessous : l'option -X n'atteint
// qu'une variable de chaîne, initialisée par une constante, dans le paquet
// main.
var version = "dev"

// versionParDefaut est ce que vaut version sans -ldflags. La comparer plutôt
// que de relire version permet à versionComposee de recevoir la sienne en
// paramètre — donc d'être testable sur ses quatre cas.
const versionParDefaut = "dev"

// versionAffichee est ce que le binaire annonce, partout : la sous-commande, le
// drapeau --version, et le pied de page d'un compte connecté. Composée une fois
// au démarrage — les réglages d'un binaire ne changent pas d'une requête à
// l'autre.
var versionAffichee = versionComposee(version, reglagesDuBuild())

// versionComposee complète une version non estampillée par la révision git et
// l'état de l'arbre au moment du build : « dev (1de2cd1, modifié) ». Une
// version estampillée, elle, se suffit — les réglages sont alors ignorés, sans
// quoi un binaire publié annoncerait une révision là où on attend un numéro.
//
// Elle reçoit ses réglages plutôt que de les lire : go test n'inscrit aucun
// réglage VCS dans ReadBuildInfo, et une fonction qui appellerait ce dernier
// elle-même ne serait vérifiable sur aucun des cas qui comptent.
func versionComposee(version string, reglages []debug.BuildSetting) string {
	if version != versionParDefaut {
		return version
	}

	revision, modifie := "", false
	for _, reglage := range reglages {
		switch reglage.Key {
		case "vcs.revision":
			revision = reglage.Value
		case "vcs.modified":
			modifie = reglage.Value == "true"
		}
	}

	if revision == "" {
		// Ni révision, ni état : il n'y a rien à dire de plus que « dev ». Un
		// arbre modifié sans révision connue ne se raccroche à rien.
		return version
	}

	if len(revision) > longueurRevisionCourte {
		revision = revision[:longueurRevisionCourte]
	}

	if modifie {
		return fmt.Sprintf("%s (%s, modifié)", version, revision)
	}
	return fmt.Sprintf("%s (%s)", version, revision)
}

// longueurRevisionCourte : ce que git affiche par défaut, et ce qu'on retrouve
// dans un git log --oneline. L'empreinte entière n'apprend rien de plus à qui
// lit un pied de page.
const longueurRevisionCourte = 7

// reglagesDuBuild rend les réglages que la chaîne de compilation a inscrits
// dans le binaire, ou rien du tout — ReadBuildInfo échoue sur un binaire qui
// n'a pas été construit en mode module.
func reglagesDuBuild() []debug.BuildSetting {
	infos, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return infos.Settings
}

// commandeVersion imprime la version et sort — le geste naturel en SSH ou dans
// un conteneur, là où le pied de page n'est pas à portée.
//
// Branchée sur la racine comme « tags fusionner », elle amorce donc
// l'application avant d'imprimer sa chaîne. C'est le prix du branchement, et il
// est assumé : une frappe à la main, pas une sonde qui tourne toutes les trente
// secondes — c'est cette différence-là qui avait fait sortir la sonde de santé
// de la racine (sante.go).
func commandeVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Affiche la version de Patachoo.",
		Args:  cobra.NoArgs,
		// Rien à imprimer d'autre que la version : une erreur d'usage garde
		// son aide, une commande qui réussit n'en a pas besoin.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionAffichee)
			return err
		},
	}
}
