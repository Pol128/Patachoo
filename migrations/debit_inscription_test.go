package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// etiquetteDInscriptionAttendue est ce que le limiteur global de PocketBase
// cherche sur POST /inscription : defaultRateLimitLabels rend
// « <méthode> <chemin> » puis « <chemin> » (apis/middlewares_rate_limit.go).
//
// Écrite en clair ici, comme le seuil et la fenêtre, plutôt que relue depuis la
// migration : un test qui compare une constante à elle-même ne vérifie que
// lui-même.
const etiquetteDInscriptionAttendue = "POST /inscription"

// Le seuil et la fenêtre, écrits en clair pour la même raison. Une heure, et
// non la minute de la connexion : une seule règle s'applique par étiquette, il
// faut donc choisir la fenêtre, et c'est la longue qui protège la table users.
const (
	seuilDInscriptionAttendu    = 10
	fenetreDInscriptionAttendue = 3600
)

// fichierDeLaMigrationDInscription sert au retour en arrière : defaitJusqua le
// désigne par son nom de fichier.
const fichierDeLaMigrationDInscription = "1789085000_debit_inscription.go"

// Rien ne plafonnait POST /inscription : l'unique règle que Patachoo ajoutait
// portait « POST /connexion », et les quatre règles livrées par PocketBase sont
// des étiquettes de collection posées sur /api/collections/… seulement. Une
// boucle curl remplissait donc la table users aussi vite que le serveur sait
// hacher.
func TestUneBaseNeuvePlafonneLaRouteDInscription(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	if !limites.Enabled {
		t.Fatal("RateLimits.Enabled est faux : aucune règle de débit ne s'applique, la nôtre comprise")
	}

	regle, posee := regleEtiquetee(limites.Rules, etiquetteDInscriptionAttendue)
	if !posee {
		t.Fatalf("aucune règle %q parmi %v", etiquetteDInscriptionAttendue, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != seuilDInscriptionAttendu {
		t.Errorf("MaxRequests = %d, attendu %d", regle.MaxRequests, seuilDInscriptionAttendu)
	}
	if regle.Duration != fenetreDInscriptionAttendue {
		t.Errorf("Duration = %d, attendu %d", regle.Duration, fenetreDInscriptionAttendue)
	}
	if regle.Audience != core.RateLimitRuleAudienceAll {
		t.Errorf("Audience = %q, attendu %q : l'attaque fabrique des comptes, et le premier jeton obtenu la ferait passer sous un plafond réservé aux invités",
			regle.Audience, core.RateLimitRuleAudienceAll)
	}
}

// La migration ajoute sa règle sans toucher aux autres : celle de la connexion
// comme les quatre livrées par PocketBase sont encore là, inchangées.
func TestLePlafondDInscriptionNeDesarmeRienDAutre(t *testing.T) {
	app := baseNeuve(t)

	limites := app.Settings().RateLimits

	regle, posee := regleEtiquetee(limites.Rules, etiquetteAttendue)
	if !posee {
		t.Fatalf("la règle %q a disparu ; restent %v", etiquetteAttendue, etiquettesDe(limites.Rules))
	}
	if regle.MaxRequests != 5 || regle.Duration != 60 {
		t.Errorf("la règle %q vaut %d requêtes par %d s, attendu 5 par 60 s",
			etiquetteAttendue, regle.MaxRequests, regle.Duration)
	}

	exigeLesReglesLivrees(t, limites.Rules)
}

// Le piège de cette migration : recopier le down de la connexion, qui éteint
// RateLimits.Enabled. Il désarmerait du même coup le plafond de POST /connexion
// et les quatre règles de PocketBase — une migration qui, en se retirant,
// ouvrirait deux portes de plus qu'elle n'en avait fermé.
func TestLeDownDuPlafondDInscriptionLaisseLeDebitArme(t *testing.T) {
	app := baseNeuve(t)

	defaitJusqua(t, app, fichierDeLaMigrationDInscription)

	limites := app.Settings().RateLimits

	if !limites.Enabled {
		t.Error("RateLimits.Enabled est faux après le down : le plafond de la connexion et les quatre règles livrées sont désarmés avec le nôtre")
	}
	if _, posee := regleEtiquetee(limites.Rules, etiquetteDInscriptionAttendue); posee {
		t.Errorf("la règle %q survit au down", etiquetteDInscriptionAttendue)
	}
	if _, posee := regleEtiquetee(limites.Rules, etiquetteAttendue); !posee {
		t.Errorf("la règle %q a disparu avec le down de l'inscription ; restent %v",
			etiquetteAttendue, etiquettesDe(limites.Rules))
	}
	exigeLesReglesLivrees(t, limites.Rules)
}
