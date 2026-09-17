package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"testing"
	"time"

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

// --- La borne de l'attente unitaire ----------------------------------------

// attenteUnitaireAttendue est ce qu'un chemin interactif accepte d'attendre le
// tour d'un hôte avant de renoncer.
//
// Écrite en clair ici, et non relue depuis cadence.go : un test qui compare une
// constante à elle-même ne vérifie que lui-même, et « une valeur codée sans
// test finit augmentée temporairement » (DOD.md §3).
const attenteUnitaireAttendue = 5 * time.Second

// hoteOccupePour place devant nous une requête en vol vers l'hôte visé : son
// tour est déjà réservé, plus loin que ce qu'un chemin interactif accepte
// d'attendre. C'est l'état d'un hôte qu'une fournée est en train de parcourir.
//
// Deux prises, et non une. La première pose seulement la dernière requête
// émise, dans le passé — l'hôte est alors poli, pas occupé, et un site poli
// doit rester joignable (TestLeCrawlDelayDuSiteNInterditPasLImportUnitaire).
// C'est la seconde qui réserve un créneau à venir, et c'est cela qu'un chemin
// interactif refuse d'attendre.
//
// Elle part sur un contexte déjà coupé : attendSonTour réserve avant
// d'attendre, donc le créneau est posé et l'horloge n'a pas bougé — le tour
// reste devant nous pour la suite du test, sans goroutine à synchroniser.
func hoteOccupePour(t *testing.T, partagee *cadence, adresse string) {
	t.Helper()

	hote := hoteDe(adresse)
	partagee.retiens(hote, attenteUnitaireAttendue+time.Second)
	if err := partagee.attendSonTour(context.Background(), hote, 0); err != nil {
		t.Fatalf("prise du tour de %q : %v", hote, err)
	}

	coupe, arrete := context.WithCancel(context.Background())
	arrete()
	if err := partagee.attendSonTour(coupe, hote, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("réservation du tour suivant de %q : %v, attendu %v", hote, err, context.Canceled)
	}
}

// Le lot attend son tour aussi longtemps qu'il le faut — il est asynchrone et
// personne n'est devant lui. L'import unitaire, lui, est le chemin interactif :
// au-delà de la borne il renonce et le dit, plutôt que de laisser tourner un
// écran. Une attente synchrone sans plafond est aussi, en soi, un levier bon
// marché : cent adresses d'un site qu'une fournée parcourt immobiliseraient
// cent gestionnaires pendant des minutes.
//
// Rien ne part : le tour n'a pas été pris, donc le site visé n'a rien vu.
func TestLImportUnitaireRenonceQuandLeTourEstTropLoin(t *testing.T) {
	_, mux, cookie, _, horloge, _ := atelierPartage(t)

	const unitaire = "https://a.example/unitaire"
	reseau := avecReseau(t, horloge, siteServi("User-agent: *\nDisallow: /prive\n", unitaire))
	hoteOccupePour(t, cadenceDeLInstance, unitaire)

	rec := importeLURL(mux, cookie, unitaire, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	corps := rec.Body.String()
	if estLeFormulaireDeRecette(corps) {
		t.Fatalf("la fiche pré-remplie est rendue alors que rien n'a été lu :\n%s", corps)
	}
	exigeContient(t, corps, `role="alert"`)
	// Le message est relu échappé : c'est le texte que l'utilisateur voit, pas
	// sa forme dans la source de la page.
	if message := html.UnescapeString(messageDErreur(t, corps)); message != messageSiteOccupe {
		t.Errorf("message %q, attendu %q", message, messageSiteOccupe)
	}
	if saisie := valeurDe(t, corps, "url"); saisie != unitaire {
		t.Errorf("champ url %q, attendu %q : l'adresse saisie est perdue", saisie, unitaire)
	}
	if appels := reseau.appels(); len(appels) != 0 {
		t.Errorf("%d requêtes émises alors que l'attente a été abandonnée :\n%v", len(appels), appels)
	}
}

// Sur POST /recettes, le renoncement ne coûte pas la recette : elle est
// enregistrée sans illustration, exactement comme quand l'image est
// injoignable. Perdre une saisie entière parce qu'un lot occupe l'hôte de
// l'image serait un remède pire que le mal.
func TestLeTourTropLoinNeCoutePasLaRecette(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	serveur := serveurDImages(t, sertLesOctets(pngDeTest(t), "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	partagee := nouvelleCadence(nouvelleHorlogeVirtuelle())
	avecCadenceDInstance(t, partagee)
	hoteOccupePour(t, partagee, serveur.URL)

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/photo.png")
	if rec := poste(t, mux, "/recettes", cookie, champs); rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if image := laRecette(t, app).GetString("image"); image != "" {
		t.Errorf("image %q enregistrée, attendue aucune : l'attente abandonnée a tout de même rapporté un fichier", image)
	}
}

// --- Ce que l'hôte réclame pour lui-même -----------------------------------

// crawlDelayAnnonce est ce qu'un site poli demande entre deux requêtes. Plus
// long que la borne de l'unitaire, et c'est tout l'intérêt : c'est la
// configuration où les deux attentes se confondent si on ne les distingue pas.
const crawlDelayAnnonce = 10 * time.Second

// robotsQuiDemande écrit le robots.txt d'un site qui annonce ce délai.
func robotsQuiDemande(d time.Duration) string {
	return fmt.Sprintf("User-agent: *\nCrawl-delay: %d\n", int(d.Seconds()))
}

// Un Crawl-delay long espace les requêtes de l'import unitaire ; il ne les fait
// pas échouer. La borne porte sur le tour d'un autre — une requête déjà en vol
// vers cet hôte —, jamais sur la politesse que le site réclame pour lui-même.
//
// Sans cette distinction, tout site annonçant plus que la borne devient
// définitivement non importable par le chemin unitaire, dès le premier import
// et sans qu'aucun lot ne tourne : le Crawl-delay est rapporté avant la requête
// de page du même appel, et c'est elle qui bute dessus. L'utilisateur lit alors
// qu'un lot occupe le site, ce qui est faux, et qu'il peut réessayer, ce qui
// n'aboutira jamais — rien ne périme ce que l'hôte a annoncé.
//
// C'est le travers que echange évite déjà pour delaiMax, pour la même raison et
// dans les mêmes termes : une attente de cadence comptée dans une borne rend
// injouable tout Crawl-delay plus long qu'elle.
func TestLeCrawlDelayDuSiteNInterditPasLImportUnitaire(t *testing.T) {
	_, mux, cookie, _, horloge, _ := atelierPartage(t)

	const unitaire = "https://poli.example/unitaire"
	reseau := avecReseau(t, horloge, siteServi(robotsQuiDemande(crawlDelayAnnonce), unitaire))

	rec := importeLURL(mux, cookie, unitaire, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if corps := rec.Body.String(); !estLeFormulaireDeRecette(corps) {
		t.Fatalf("la fiche pré-remplie n'est pas rendue alors que le site n'a rien refusé :\n%s", corps)
	}

	appels := reseau.appels()
	if len(appels) != 2 {
		t.Fatalf("%d requêtes émises, attendu 2 — le robots.txt puis la page :\n%v", len(appels), appels)
	}
	// Espacées de ce que le site demande : sa politesse est tenue, pas
	// contournée. Mesuré sur l'horloge virtuelle, en quelques microsecondes.
	if ecart := appels[1].instant.Sub(appels[0].instant); ecart != crawlDelayAnnonce {
		t.Errorf("robots.txt et page espacés de %v, attendu %v", ecart, crawlDelayAnnonce)
	}
}

// Le pendant sur POST /recettes : un site qui demande dix secondes entre deux
// requêtes ne coûte pas l'illustration. Elle part plus tard, elle part.
func TestLeCrawlDelayDuSiteNeCoutePasLIllustration(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	serveur := serveurDImagesRobots(t, robotsQuiDemande(crawlDelayAnnonce), sertLesOctets(pngDeTest(t), "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/photo.png")
	if rec := poste(t, mux, "/recettes", cookie, champs); rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if image := laRecette(t, app).GetString("image"); image == "" {
		t.Error("aucune image enregistrée : le Crawl-delay du site a été lu comme un hôte occupé")
	}
}
