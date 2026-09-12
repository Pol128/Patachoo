package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// etiquetteAttendueDeLAPI est l'étiquette primaire que checkCollectionRateLimit
// cherche en deuxième position sur POST /api/collections/users/auth-with-password
// — l'ordre est users:authWithPassword, users:auth, *:authWithPassword, *:auth,
// puis les étiquettes de chemin (apis/middlewares_rate_limit.go).
//
// C'est celle-là, et non *:auth : *:auth couvre aussi
// /api/collections/_superusers/auth-with-password, et la relever relèverait du
// même coup le plafond de l'authentification superuser.
//
// Écrite en clair ici, comme le seuil et la fenêtre, plutôt que relue depuis la
// migration : un test qui compare une constante à elle-même ne vérifie que
// lui-même.
const etiquetteAttendueDeLAPI = "users:auth"

// fichierDeLaMigrationDeLAPI sert au retour en arrière : defaitJusqua désigne
// une migration par son nom de fichier.
const fichierDeLaMigrationDeLAPI = "1789245300_debit_api_auth.go"

// POST /api/collections/users/auth-with-password accepte exactement les mêmes
// identifiants que notre formulaire, mais tombait sur la règle *:auth livrée par
// PocketBase — 2 requêtes par 3 s, soit 40 essais de mot de passe par minute là
// où POST /connexion en autorise 5. Le seuil et la fenêtre sont ici en clair :
// DOD.md §3, « une valeur codée sans test finit augmentée temporairement ».
func TestUneBaseNeuvePlafonneLAuthentificationParLAPI(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	if !limites.Enabled {
		t.Fatal("RateLimits.Enabled est faux : aucune règle de débit ne s'applique, la nôtre comprise")
	}

	regle, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLAPI)
	if !posee {
		t.Fatalf("aucune règle %q parmi %v", etiquetteAttendueDeLAPI, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != 5 {
		t.Errorf("MaxRequests = %d, attendu 5 : le plafond de la route de l'API doit valoir celui de POST /connexion", regle.MaxRequests)
	}
	if regle.Duration != 60 {
		t.Errorf("Duration = %d, attendu 60", regle.Duration)
	}
	if regle.Audience != core.RateLimitRuleAudienceAll {
		t.Errorf("Audience = %q, attendu %q : une session déjà ouverte doit compter comme les autres",
			regle.Audience, core.RateLimitRuleAudienceAll)
	}
}

// Ajout, et non remplacement : les quatre règles livrées par PocketBase et
// celle de POST /connexion restent. *:auth en particulier, et avec ses valeurs
// d'origine — c'est elle, et elle seule, qui plafonne encore
// l'authentification superuser, faute de règle _superusers:auth.
func TestLaRegleDeLAPINEmporteAucuneAutre(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLAPI); !posee {
		t.Fatalf("aucune règle %q parmi %v : rien n'a été ajouté, il n'y a rien à dire de ce qui survit",
			etiquetteAttendueDeLAPI, etiquettesDe(limites.Rules))
	}

	exigeLesReglesLivrees(t, limites.Rules)
	// etiquetteAttendue est « POST /connexion », déclarée par
	// debit_connexion_test.go : le plafond de la porte d'à côté ne bouge pas.
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendue); !posee {
		t.Errorf("la règle %q a disparu ; restent %v", etiquetteAttendue, etiquettesDe(limites.Rules))
	}

	joker, posee := regleEtiquetee(limites.Rules, "*:auth")
	if !posee {
		t.Fatalf("la règle *:auth livrée par PocketBase a disparu ; restent %v", etiquettesDe(limites.Rules))
	}
	if joker.MaxRequests != 2 || joker.Duration != 3 {
		t.Errorf("*:auth vaut %d requêtes par %d s, attendu 2 par 3 : c'est elle qui plafonne l'authentification superuser, et la relever serait la faute que users:auth évite",
			joker.MaxRequests, joker.Duration)
	}
}

// Le down ne retire que la sienne, et surtout il ne touche pas à Enabled :
// l'éteindre rouvrirait la route de connexion, que 1788993000_debit_connexion
// protège. Une migration qui défait le travail d'une autre n'est pas
// réversible, elle est destructrice.
func TestLeDownDuDebitDeLAPINeRetireQueSaRegle(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaMigrationDeLAPI)

	limites := app.Settings().RateLimits

	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendueDeLAPI); posee {
		t.Errorf("la règle %q survit au down", etiquetteAttendueDeLAPI)
	}
	if !limites.Enabled {
		t.Error("RateLimits.Enabled est faux après le down : la route de connexion n'a plus de plafond")
	}
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendue); !posee {
		t.Errorf("la règle %q a disparu avec le down de l'API ; restent %v", etiquetteAttendue, etiquettesDe(limites.Rules))
	}
	exigeLesReglesLivrees(t, limites.Rules)
}
