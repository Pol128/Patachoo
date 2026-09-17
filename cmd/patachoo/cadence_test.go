package main

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// La cadence est celle de l'instance, et non celle de l'ouvrier : le lot et
// les chemins unitaires — l'import d'une URL, l'image que le formulaire fait
// télécharger — passent par le même tour de rôle, hôte par hôte.
//
// Sans ce partage, l'import unitaire est le seul chemin sortant du produit qui
// ne passe par aucun tour de rôle : une boucle de POST /recettes/importer vers
// la même cible part à la cadence que l'appelant veut, sous notre adresse et
// sous notre nom, et le disclaimer du lot — « le rythme est tenu » — devient
// faux pour tout le reste du produit.
//
// Tout se mesure ici sur l'horloge virtuelle de ouvrier_test.go : c'est elle
// qui permet de lire un écart d'une seconde en quelques microsecondes.

// avecCadenceDInstance installe, le temps du test, le tour de rôle que
// partagent l'ouvrier et les chemins unitaires.
//
// Une cadence par test, et non celle de production : la sienne est bâtie sur
// l'horloge du système, et ses entrées survivent à un test — deux requêtes du
// même paquet de tests vers 127.0.0.1 attendraient une seconde réelle.
func avecCadenceDInstance(t *testing.T, partagee *cadence) {
	t.Helper()

	precedent := cadenceDeLInstance
	cadenceDeLInstance = partagee
	t.Cleanup(func() { cadenceDeLInstance = precedent })
}

// atelierPartage monte le carnet, la cadence de l'instance sur l'horloge
// virtuelle, et l'ouvrier qui la reçoit — c'est-à-dire le montage de
// production, où une seule cadence sert les deux chemins.
func atelierPartage(t *testing.T) (core.App, http.Handler, *http.Cookie, *core.Record, *horlogeVirtuelle, *ouvrier) {
	t.Helper()

	app, mux, cookie := carnetDeTest(t)

	titulaire, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("relecture du compte de test : %v", err)
	}

	horloge := nouvelleHorlogeVirtuelle()
	partagee := nouvelleCadence(horloge)
	avecCadenceDInstance(t, partagee)

	return app, mux, cookie, titulaire, horloge, nouvelOuvrier(app, horloge, partagee)
}

// Le lot et l'import unitaire sur un même hôte se suivent, au lieu de partir
// ensemble : une seule cadence les ordonne, et l'écart entre deux requêtes
// sortantes vaut au moins notre politesse.
//
// Le compte se fait au transport, et non à la couture recuperePage : un appel
// de recuperation émet deux requêtes — le robots.txt de l'hôte, puis la page —
// et c'est sur celles-là que porte la promesse.
func TestLeLotEtLImportUnitaireSeSerialisentSurUnMemeHote(t *testing.T) {
	app, mux, cookie, titulaire, horloge, o := atelierPartage(t)

	const duLot = "https://a.example/du-lot"
	const unitaire = "https://a.example/unitaire"
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", duLot, unitaire))

	traite(t, o, lotDe(t, app, titulaire, duLot))

	if rec := importeLURL(mux, cookie, unitaire, nil); rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour l'import unitaire, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	appels := reseau.appels()
	// Le robots.txt et la page du lot, puis le robots.txt et la page de
	// l'unitaire : celui-ci ne tient pas le cache de robots.txt de la fournée.
	if len(appels) != 4 {
		t.Fatalf("%d requêtes émises, attendu 4 — deux par appel :\n%v", len(appels), appels)
	}
	for i, ecart := range ecartsEntre(appels) {
		if ecart < delaiEntreRequetes {
			t.Errorf("requêtes %q et %q espacées de %v, attendu au moins %v — le chemin unitaire n'attend pas son tour",
				appels[i].url, appels[i+1].url, ecart, delaiEntreRequetes)
		}
	}
}

// Le tour de rôle est par hôte : un lot en cours sur un site ne fait pas
// patienter l'import unitaire d'un autre site. Sans cela, une cadence partagée
// serait une file d'attente unique, et le produit entier n'émettrait plus
// qu'une requête par seconde.
func TestUnImportUnitaireNAttendPasLeLotDUnAutreHote(t *testing.T) {
	app, mux, cookie, titulaire, horloge, o := atelierPartage(t)

	const duLot = "https://a.example/du-lot"
	const unitaire = "https://b.example/unitaire"
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", duLot, unitaire))

	traite(t, o, lotDe(t, app, titulaire, duLot))

	if rec := importeLURL(mux, cookie, unitaire, nil); rec.Code != http.StatusOK {
		t.Fatalf("statut %d pour l'import unitaire, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	versA := reseau.appelsVers("a.example")
	versB := reseau.appelsVers("b.example")
	if len(versA) != 2 || len(versB) != 2 {
		t.Fatalf("%d requêtes vers a.example et %d vers b.example, attendu deux de chaque :\n%v",
			len(versA), len(versB), reseau.appels())
	}

	// La première requête vers b.example part dans l'instant où la dernière
	// vers a.example est partie : le tour de l'un ne se prend pas sur l'autre.
	derniereVersA := versA[len(versA)-1].instant
	if attente := versB[0].instant.Sub(derniereVersA); attente != 0 {
		t.Errorf("l'import unitaire vers b.example a attendu %v après la dernière requête vers a.example, attendu 0 : les hôtes s'attendent entre eux",
			attente)
	}
}
