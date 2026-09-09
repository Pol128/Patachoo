package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// etiquetteAttendue est l'étiquette que le limiteur global de PocketBase
// cherche sur notre route : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// Écrite en clair ici, comme le seuil et la fenêtre, plutôt que relue depuis la
// migration : un test qui compare une constante à elle-même ne vérifie que
// lui-même.
const etiquetteAttendue = "POST /connexion"

// fichierDeLaMigration sert au retour en arrière : defaitJusqua le désigne par
// son nom de fichier.
const fichierDeLaMigration = "1788993000_debit_connexion.go"

// RateLimits.Enabled vaut false par défaut chez PocketBase
// (core/settings_model.go), donc aucune route n'a de plafond sur une
// installation fraîche — pas même celle qui calcule un bcrypt à chaque appel.
func TestUneBaseNeuvePlafonneLaRouteDeConnexion(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	if !limites.Enabled {
		t.Fatal("RateLimits.Enabled est faux : aucune règle de débit ne s'applique, la nôtre comprise")
	}

	regle, posee := regleEtiquetee(limites.Rules, etiquetteAttendue)
	if !posee {
		t.Fatalf("aucune règle %q parmi %v", etiquetteAttendue, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != 5 {
		t.Errorf("MaxRequests = %d, attendu 5", regle.MaxRequests)
	}
	if regle.Duration != 60 {
		t.Errorf("Duration = %d, attendu 60", regle.Duration)
	}
	if regle.Audience != core.RateLimitRuleAudienceAll {
		t.Errorf("Audience = %q, attendu %q : une session déjà ouverte doit compter comme les autres",
			regle.Audience, core.RateLimitRuleAudienceAll)
	}
}

// La migration ajoute sa règle, elle ne remplace pas la liste : les quatre
// règles livrées par PocketBase protègent l'API REST, et les écraser en
// passant reviendrait à ouvrir une porte en en fermant une autre.
func TestLesReglesLivreesParPocketBaseSurvivent(t *testing.T) {
	app := baseNeuve(t)

	exigeLesReglesLivrees(t, app.Settings().RateLimits.Rules)
}

// Une migration qui ne sait pas revenir en arrière n'est pas relisible : on ne
// peut pas l'essayer sur une base et la retirer. Elle ne retire que la sienne.
func TestLeDownRetireLaSeuleRegleDeConnexion(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaMigration)

	limites := app.Settings().RateLimits

	if limites.Enabled {
		t.Error("RateLimits.Enabled est encore vrai après le down")
	}
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendue); posee {
		t.Errorf("la règle %q survit au down", etiquetteAttendue)
	}
	exigeLesReglesLivrees(t, limites.Rules)
}

// exigeLesReglesLivrees vérifie que les quatre règles de PocketBase sont là.
func exigeLesReglesLivrees(t *testing.T, regles []core.RateLimitRule) {
	t.Helper()

	for _, etiquette := range []string{"*:auth", "*:create", "/api/batch", "/api/"} {
		if _, posee := regleEtiquetee(regles, etiquette); !posee {
			t.Errorf("la règle %q livrée par PocketBase a disparu ; restent %v", etiquette, etiquettesDe(regles))
		}
	}
}

// regleEtiquetee rend la règle portant cette étiquette, s'il y en a une.
func regleEtiquetee(regles []core.RateLimitRule, etiquette string) (core.RateLimitRule, bool) {
	for _, regle := range regles {
		if regle.Label == etiquette {
			return regle, true
		}
	}
	return core.RateLimitRule{}, false
}

// etiquettesDe rend les étiquettes en place, pour les messages d'échec.
func etiquettesDe(regles []core.RateLimitRule) []string {
	etiquettes := make([]string, 0, len(regles))
	for _, regle := range regles {
		etiquettes = append(etiquettes, regle.Label)
	}
	return etiquettes
}
