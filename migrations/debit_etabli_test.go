package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// etiquetteAttendueDeLEtabli est ce que le limiteur global de PocketBase
// cherche sur la route de lancement : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// La forme longue, et non le chemin nu : le GET de la page n'écrit rien, et le
// plafonner mettrait hors de portée de celui qui vient de dépasser son quota
// la page même qui le lui dit — avec, en plus, le suivi de la passe en cours.
//
// Écrite en clair ici, comme le seuil et la fenêtre, plutôt que relue depuis la
// migration : un test qui compare une constante à elle-même ne vérifie que
// lui-même.
const etiquetteAttendueDeLEtabli = "POST /etabli"

// fichierDeLaMigrationDeLEtabli sert au retour en arrière : defaitJusqua
// désigne une migration par son nom de fichier.
const fichierDeLaMigrationDeLEtabli = "1790006400_debit_etabli.go"

// Sans plafond, rien ne borne le volume qu'une suite de lancements fait tenir
// en mémoire. Le POST de l'établi lit le corps sous le plafond de l'analyse —
// 32 Mio de parties non-fichier, quand le reste du produit s'arrête à 8 —, la
// recopie que le routeur fait de tout corps lu s'y ajoute, puis la lecture du
// corpus par le gestionnaire ; et tout cela est alloué avant le refus « une
// analyse à la fois », qui ne se prononce qu'à la mise en file. Le sérialisateur
// de l'ouvrier ne borne donc rien du coût des requêtes, seulement celui des
// passes.
//
// Le seuil et la fenêtre sont ici en clair : DOD.md §3, « une valeur codée sans
// test finit augmentée temporairement ».
func TestUneBaseNeuvePlafonneLeLancementDUneAnalyse(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	regle, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLEtabli)
	if !posee {
		t.Fatalf("aucune règle %q parmi %v", etiquetteAttendueDeLEtabli, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != 5 {
		t.Errorf("MaxRequests = %d, attendu 5", regle.MaxRequests)
	}
	if regle.Duration != 60 {
		t.Errorf("Duration = %d, attendu 60", regle.Duration)
	}
	if regle.Audience != core.RateLimitRuleAudienceAuth {
		t.Errorf("Audience = %q, attendu %q : la route est derrière exigeUnCurateur, et lui seul fait lire le corps",
			regle.Audience, core.RateLimitRuleAudienceAuth)
	}
}

// Le down ne retire que la sienne, et surtout il ne touche pas à Enabled :
// l'éteindre rouvrirait la route de connexion, que 1788993000_debit_connexion
// protège. Une migration qui défait le travail d'une autre n'est pas
// réversible, elle est destructrice.
func TestLeDownDuDebitDeLEtabliNeRetireQueSaRegle(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaMigrationDeLEtabli)

	limites := app.Settings().RateLimits

	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLEtabli); posee {
		t.Errorf("la règle %q survit au down", etiquetteAttendueDeLEtabli)
	}
	if !limites.Enabled {
		t.Error("RateLimits.Enabled est faux après le down : la route de connexion n'a plus de plafond")
	}
	// etiquetteAttendueDuLot est la règle voisine, déclarée par
	// debit_lot_test.go : celle qu'il ne faut pas emporter.
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDuLot); !posee {
		t.Errorf("la règle %q a disparu avec le down de l'établi ; restent %v",
			etiquetteAttendueDuLot, etiquettesDe(limites.Rules))
	}
	exigeLesReglesLivrees(t, limites.Rules)
}
