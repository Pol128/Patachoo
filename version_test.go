package main

import (
	"net/http"
	"runtime/debug"
	"strings"
	"testing"
)

// --- La composition d'une version non estampillée ------------------------

// Les quatre cas de versionComposee. Elle reçoit ses réglages plutôt que de
// les lire : go test n'inscrit aucun réglage VCS dans ReadBuildInfo, donc une
// fonction qui les lirait elle-même ne serait testable sur aucun cas.
func TestVersionComposeeCompleteUnBinaireNonEstampille(t *testing.T) {
	// La révision telle que l'éditeur de liens l'inscrit : l'empreinte
	// entière. C'est la version affichée qui la raccourcit.
	const revision = "1de2cd1a2b3c4d5e6f708192a3b4c5d6e7f80912"

	cas := []struct {
		nom      string
		version  string
		reglages []debug.BuildSetting
		attendu  string
	}{
		{
			nom:     "aucun réglage VCS : la version nue",
			version: "dev",
			attendu: "dev",
		},
		{
			nom:      "révision seule",
			version:  "dev",
			reglages: []debug.BuildSetting{{Key: "vcs.revision", Value: revision}},
			attendu:  "dev (1de2cd1)",
		},
		{
			nom:     "révision et arbre modifié",
			version: "dev",
			reglages: []debug.BuildSetting{
				{Key: "vcs.revision", Value: revision},
				{Key: "vcs.modified", Value: "true"},
			},
			attendu: "dev (1de2cd1, modifié)",
		},
		{
			nom:     "version estampillée : les réglages sont ignorés",
			version: "0.1.0",
			reglages: []debug.BuildSetting{
				{Key: "vcs.revision", Value: revision},
				{Key: "vcs.modified", Value: "true"},
			},
			attendu: "0.1.0",
		},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			if obtenu := versionComposee(c.version, c.reglages); obtenu != c.attendu {
				t.Errorf("versionComposee(%q, %v) = %q, attendu %q", c.version, c.reglages, obtenu, c.attendu)
			}
		})
	}
}

// Le défaut se perdrait à la première refonte du build : sans -ldflags, un
// binaire dit « dev », et c'est ce qui distingue une construction locale d'une
// version publiée.
func TestLaVersionParDefautEstDev(t *testing.T) {
	if version != "dev" {
		t.Errorf("version = %q, attendu %q — les tests tournent sans -ldflags", version, "dev")
	}
}

// --- La sous-commande ----------------------------------------------------

// Le geste naturel en SSH ou dans un conteneur : imprimer la version et sortir.
func TestLaCommandeVersionImprimeLaVersionEtSort(t *testing.T) {
	app := baseNeuve(t)

	sortie, aEchoue, err := executeLaCommande(t, app, "version")
	if err != nil {
		t.Fatalf("patachoo version : %v", err)
	}
	if aEchoue {
		t.Error("patachoo version sort en échec")
	}

	imprime := strings.TrimSpace(sortie)
	if imprime != versionAffichee {
		t.Errorf("sortie %q, attendu %q", imprime, versionAffichee)
	}
	// Les tests tournent sur un binaire non estampillé : ce que la commande
	// imprime commence donc par « dev ».
	if !strings.HasPrefix(imprime, "dev") {
		t.Errorf("sortie %q, attendu une version commençant par « dev »", imprime)
	}
}

// PocketBase pose Version = « (untracked) » sur la racine cobra, ce qui suffit
// à cobra pour ajouter --version. Sans y toucher, le binaire livrerait deux
// réponses contradictoires à la même question.
func TestLaRacineAnnonceLaMemeVersionQueLaSousCommande(t *testing.T) {
	app := baseNeuve(t)

	sousCommande, _, err := executeLaCommande(t, app, "version")
	if err != nil {
		t.Fatalf("patachoo version : %v", err)
	}

	drapeau, aEchoue, err := executeLaCommande(t, app, "--version")
	if err != nil {
		t.Fatalf("patachoo --version : %v", err)
	}
	if aEchoue {
		t.Error("patachoo --version sort en échec")
	}

	if !strings.Contains(drapeau, strings.TrimSpace(sousCommande)) {
		t.Errorf("patachoo --version rend %q, sans la version %q de la sous-commande",
			drapeau, strings.TrimSpace(sousCommande))
	}
	if strings.Contains(drapeau, "(untracked)") {
		t.Errorf("patachoo --version rend la version de PocketBase : %q", drapeau)
	}
}

// --- Le pied de page -----------------------------------------------------

// La version est une information d'exploitation : elle s'affiche à qui
// administre l'instance, en lien vers les versions publiées.
func TestLePiedDePagePorteLaVersionPourUnCompteConnecte(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	corps := avecCookie(mux, http.MethodGet, "/recettes", cookie).Body.String()

	pied := entreBalises(corps, "<footer>", "</footer>")
	if strings.TrimSpace(pied) == "" {
		t.Fatalf("aucun pied de page dans :\n%s", corps)
	}
	if !strings.Contains(pied, versionAffichee) {
		t.Errorf("pied de page sans la version %q : %q", versionAffichee, pied)
	}
	if !strings.Contains(pied, `href="https://github.com/Pol128/Patachoo/releases"`) {
		t.Errorf("pied de page sans lien vers les versions publiées : %q", pied)
	}
}

// Et pas à qui frappe à la porte : un visiteur non authentifié n'a pas à lire
// à quelle faille répond l'instance qu'il regarde. Son pied ne bouge pas.
func TestLePiedDePageDUnVisiteurNePorteNiVersionNiLien(t *testing.T) {
	_, mux := serveurDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/connexion", nil).Body.String()

	pied := entreBalises(corps, "<footer>", "</footer>")
	if pied != "Patachoo — carnet de recettes" {
		t.Errorf("pied de page d'un visiteur : %q, attendu %q", pied, "Patachoo — carnet de recettes")
	}
}
