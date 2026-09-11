package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// etiquetteAttendueDuLot est ce que le limiteur global de PocketBase cherche
// sur la route de lancement : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// La forme longue, et non le chemin nu : le GET de la page de saisie n'écrit
// rien et n'a pas à être plafonné avec le POST qui, lui, remplit la base.
//
// Écrite en clair ici, comme le seuil et la fenêtre, plutôt que relue depuis la
// migration : un test qui compare une constante à elle-même ne vérifie que
// lui-même.
const etiquetteAttendueDuLot = "POST /recettes/importer/lot"

// fichierDeLaMigrationDuLot sert au retour en arrière : defaitJusqua désigne
// une migration par son nom de fichier.
const fichierDeLaMigrationDuLot = "1789158900_debit_lot.go"

// Sans plafond, une session suffit à écrire 50 000 lignes import_urls et 100
// tags par minute — les tags étant globaux, la liste devient inutilisable pour
// tous les comptes. Le seuil et la fenêtre sont ici en clair : DOD.md §3,
// « une valeur codée sans test finit augmentée temporairement ».
func TestUneBaseNeuvePlafonneLeLancementDUnLot(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	regle, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDuLot)
	if !posee {
		t.Fatalf("aucune règle %q parmi %v", etiquetteAttendueDuLot, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != 5 {
		t.Errorf("MaxRequests = %d, attendu 5", regle.MaxRequests)
	}
	if regle.Duration != 60 {
		t.Errorf("Duration = %d, attendu 60", regle.Duration)
	}
	if regle.Audience != core.RateLimitRuleAudienceAuth {
		t.Errorf("Audience = %q, attendu %q : la route est derrière exigeUneSession, un visiteur n'atteint jamais le gestionnaire",
			regle.Audience, core.RateLimitRuleAudienceAuth)
	}
}

// Le down ne retire que la sienne, et surtout il ne touche pas à Enabled :
// l'éteindre rouvrirait la route de connexion, que la migration précédente
// protège. Une migration qui défait le travail d'une autre n'est pas réversible,
// elle est destructrice.
func TestLeDownDuDebitDuLotNeRetireQueSaRegle(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaMigrationDuLot)

	limites := app.Settings().RateLimits

	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDuLot); posee {
		t.Errorf("la règle %q survit au down", etiquetteAttendueDuLot)
	}
	if !limites.Enabled {
		t.Error("RateLimits.Enabled est faux après le down : la route de connexion n'a plus de plafond")
	}
	// etiquetteAttendue est « POST /connexion », déclarée par
	// debit_connexion_test.go : c'est la règle voisine, celle qu'il ne faut pas
	// emporter.
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendue); !posee {
		t.Errorf("la règle %q a disparu avec le down du lot ; restent %v", etiquetteAttendue, etiquettesDe(limites.Rules))
	}
	exigeLesReglesLivrees(t, limites.Rules)
}
