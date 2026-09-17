package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// etiquetteAttendueDeLImport est ce que le limiteur global de PocketBase
// cherche sur la route de l'import unitaire : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// La forme longue, et non le chemin nu : une règle « /recettes/importer »
// plafonnerait aussi le GET de la page de saisie, c'est-à-dire la page même que
// le refusé doit voir.
//
// Écrite en clair ici, comme le seuil et la fenêtre, plutôt que relue depuis la
// migration : un test qui compare une constante à elle-même ne vérifie que
// lui-même.
const etiquetteAttendueDeLImport = "POST /recettes/importer"

// fichierDeLaMigrationDeLImport sert au retour en arrière : defaitJusqua
// désigne une migration par son nom de fichier.
const fichierDeLaMigrationDeLImport = "1789660000_debit_import.go"

// Sans plafond, un compte ordinaire poste POST /recettes/importer en boucle sur
// la même URL cible, et chaque requête entrante en produit deux sortantes — le
// robots.txt de l'hôte, puis la page. Le site visé n'en voit qu'une chose :
// notre adresse et notre nom. Le seuil et la fenêtre sont ici en clair :
// DOD.md §3, « une valeur codée sans test finit augmentée temporairement ».
func TestUneBaseNeuvePlafonneLImportUnitaire(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	if !limites.Enabled {
		t.Fatal("RateLimits.Enabled est faux : aucune règle de débit ne s'applique, la nôtre comprise")
	}

	regle, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLImport)
	if !posee {
		t.Fatalf("aucune règle %q parmi %v", etiquetteAttendueDeLImport, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != 10 {
		t.Errorf("MaxRequests = %d, attendu 10", regle.MaxRequests)
	}
	if regle.Duration != 60 {
		t.Errorf("Duration = %d, attendu 60", regle.Duration)
	}
	if regle.Audience != core.RateLimitRuleAudienceAuth {
		t.Errorf("Audience = %q, attendu %q : la route est derrière exigeUneSession, un visiteur n'atteint jamais le gestionnaire",
			regle.Audience, core.RateLimitRuleAudienceAuth)
	}
}

// Ajout, et non remplacement. La règle du lot en particulier : son étiquette
// « POST /recettes/importer/lot » commence par celle-ci, et FindRateLimitRule
// ne cherche un préfixe que pour les étiquettes finissant par « / »
// (core/settings_model.go). Les deux plafonds restent donc distincts, avec
// leurs seuils propres — et c'est ce que ce test fige.
func TestLaRegleDeLImportNEmportePasCelleDuLot(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLImport); !posee {
		t.Fatalf("aucune règle %q parmi %v : rien n'a été ajouté, il n'y a rien à dire de ce qui survit",
			etiquetteAttendueDeLImport, etiquettesDe(limites.Rules))
	}

	lot, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDuLot)
	if !posee {
		t.Fatalf("la règle %q a disparu ; restent %v", etiquetteAttendueDuLot, etiquettesDe(limites.Rules))
	}
	if lot.MaxRequests != 5 || lot.Duration != 60 {
		t.Errorf("%q vaut %d requêtes par %d s, attendu 5 par 60 : le plafond du lot a bougé avec celui de l'import",
			etiquetteAttendueDuLot, lot.MaxRequests, lot.Duration)
	}
	// etiquetteAttendue est « POST /connexion », déclarée par
	// debit_connexion_test.go.
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendue); !posee {
		t.Errorf("la règle %q a disparu ; restent %v", etiquetteAttendue, etiquettesDe(limites.Rules))
	}
	exigeLesReglesLivrees(t, limites.Rules)
}

// Le down ne retire que la sienne, et surtout il ne touche pas à Enabled :
// l'éteindre rouvrirait POST /connexion, POST /inscription, le lancement d'un
// lot et l'authentification par l'API. Une migration qui défait le travail de
// quatre autres n'est pas réversible, elle est destructrice.
func TestLeDownDuDebitDeLImportNeRetireQueSaRegle(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaMigrationDeLImport)

	limites := app.Settings().RateLimits

	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLImport); posee {
		t.Errorf("la règle %q survit au down", etiquetteAttendueDeLImport)
	}
	if !limites.Enabled {
		t.Error("RateLimits.Enabled est faux après le down : quatre routes n'ont plus de plafond")
	}
	for _, voisine := range []string{
		etiquetteAttendue,
		etiquetteDInscriptionAttendue,
		etiquetteAttendueDuLot,
		etiquetteAttendueDeLAPI,
	} {
		if _, posee := regleEtiquetee(limites.Rules, voisine); !posee {
			t.Errorf("la règle %q a disparu avec le down de l'import ; restent %v", voisine, etiquettesDe(limites.Rules))
		}
	}
	exigeLesReglesLivrees(t, limites.Rules)
}
