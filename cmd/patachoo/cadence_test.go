package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
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
// émise, dans le passé : le tour suivant est alors à une seconde, et personne
// n'est devant nous. C'est la seconde qui réserve un créneau à venir, et c'est
// cela qu'un chemin interactif refuse d'attendre — l'hôte qu'une fournée
// parcourt, par opposition à l'hôte qui réclame de longs délais pour lui-même
// (TestLeCrawlDelayPlusLongQueLaBorneRefuseLImportUnitaire).
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
	if message := html.UnescapeString(messageDErreur(t, corps)); message != messageTourTropLoin {
		t.Errorf("message %q, attendu %q", message, messageTourTropLoin)
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
// configuration où la borne se déclenche sans qu'aucun lot ne tourne, et où le
// site visé décide seul de la durée de notre attente.
const crawlDelayAnnonce = 10 * time.Second

// robotsQuiDemande écrit le robots.txt d'un site qui annonce ce délai.
func robotsQuiDemande(d time.Duration) string {
	return fmt.Sprintf("User-agent: *\nCrawl-delay: %d\n", int(d.Seconds()))
}

// Sur un chemin interactif, aucune attente de cadence ne dépasse la borne,
// quelle qu'en soit l'origine : un Crawl-delay plus long qu'elle fait renoncer
// l'import unitaire, exactement comme un lot en cours.
//
// Ce n'est pas la même chose que de laisser le site décider : le Crawl-delay est
// rapporté avant la requête de page du même appel, il n'entre dans aucun budget
// de temps (echange prend le tour de rôle hors de delaiMax), et delaiAnnonceMax
// accepte jusqu'à cinq minutes. Sans cette borne, un serveur qui annonce
// « Crawl-delay: 300 » immobilise un gestionnaire HTTP cinq minutes par hôte
// importé — le levier bon marché que la tâche nomme, et que le plafond de dix
// imports par minute ne ferme pas, un sous-domaine par import suffisant à en
// changer.
//
// Ce que ce renoncement coûte, et qui est assumé : un site annonçant plus que
// la borne n'est pas importable à la main. L'import en lot, lui, l'attend sans
// limite — c'est ce que le message doit dire, et ce que la borne laisse ouvert.
func TestLeCrawlDelayPlusLongQueLaBorneRefuseLImportUnitaire(t *testing.T) {
	_, mux, cookie, _, horloge, _ := atelierPartage(t)

	const unitaire = "https://poli.example/unitaire"
	reseau := avecReseau(t, horloge, siteServi(robotsQuiDemande(crawlDelayAnnonce), unitaire))

	depart := horloge.Maintenant()
	rec := importeLURL(mux, cookie, unitaire, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	// Mesuré sur l'horloge virtuelle : la borne se lit en quelques
	// microsecondes, et aucun test n'attend cinq secondes.
	if ecoule := horloge.Maintenant().Sub(depart); ecoule >= attenteUnitaireAttendue {
		t.Errorf("l'appel a passé %v en attente de cadence, attendu moins de %v : le site visé décide de la durée du gestionnaire",
			ecoule, attenteUnitaireAttendue)
	}

	corps := rec.Body.String()
	if estLeFormulaireDeRecette(corps) {
		t.Fatalf("la fiche pré-remplie est rendue alors que la page n'a pas été lue :\n%s", corps)
	}
	exigeContient(t, corps, `role="alert"`)
	if message := html.UnescapeString(messageDErreur(t, corps)); message != messageTourTropLoin {
		t.Errorf("message %q, attendu %q", message, messageTourTropLoin)
	}
	if saisie := valeurDe(t, corps, "url"); saisie != unitaire {
		t.Errorf("champ url %q, attendu %q : l'adresse saisie est perdue", saisie, unitaire)
	}
	// Le robots.txt est parti — c'est lui qui a rapporté l'annonce —, la page
	// non : son tour était trop loin, et le créneau n'a pas été pris.
	if appels := reseau.appels(); len(appels) != 1 {
		t.Errorf("%d requêtes émises, attendu 1 — le robots.txt seul :\n%v", len(appels), appels)
	}
}

// Deux imports successifs vers le même hôte poli, et c'est le second qui est
// figé ici : un test qui n'en joue qu'un ne peut pas voir ce que le suivant
// raconte.
//
// Le second ne sort même pas : l'annonce est retenue, rien ne la périme, et son
// robots.txt bute déjà sur la borne. Le refus est donc stable, et c'est
// précisément ce qu'un message ne doit pas présenter comme passager.
func TestLeSecondImportDUnHotePoliRendLeMemeRefus(t *testing.T) {
	_, mux, cookie, _, horloge, _ := atelierPartage(t)

	const unitaire = "https://poli.example/unitaire"
	reseau := avecReseau(t, horloge, siteServi(robotsQuiDemande(crawlDelayAnnonce), unitaire))

	if rec := importeLURL(mux, cookie, unitaire, nil); rec.Code != http.StatusOK {
		t.Fatalf("statut %d au premier import, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	depart := horloge.Maintenant()
	second := importeLURL(mux, cookie, unitaire, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("statut %d au second import, attendu %d :\n%s", second.Code, http.StatusOK, second.Body.String())
	}
	if ecoule := horloge.Maintenant().Sub(depart); ecoule >= attenteUnitaireAttendue {
		t.Errorf("le second import a passé %v en attente de cadence, attendu moins de %v", ecoule, attenteUnitaireAttendue)
	}

	corps := second.Body.String()
	if estLeFormulaireDeRecette(corps) {
		t.Fatalf("la fiche pré-remplie est rendue au second import :\n%s", corps)
	}
	if message := html.UnescapeString(messageDErreur(t, corps)); message != messageTourTropLoin {
		t.Errorf("message du second import %q, attendu %q", message, messageTourTropLoin)
	}
	// Une seule requête en tout, celle du premier appel : le second n'a rien
	// envoyé au site.
	if appels := reseau.appels(); len(appels) != 1 {
		t.Errorf("%d requêtes émises pour deux imports, attendu 1 :\n%v", len(appels), appels)
	}
}

// Le message du renoncement n'affirme aucune cause que le code ne connaît pas.
// recuperation.AttenteDeCadence ne distingue pas « un lot parcourt cet hôte »
// de « cet hôte réclame de longs délais » : nommer le lot est donc faux une fois
// sur deux, et « réessayez dans un instant » promet un aboutissement que rien ne
// tient — l'annonce d'un hôte ne se périme pas.
//
// Les deux affirmations sont citées en clair, et non relues depuis la constante :
// comparer le rendu à la constante ne fige rien, puisque la réécrire changerait
// les deux côtés à la fois.
func TestLeMessageDuRenoncementNAffirmeNiLotNiNouvelEssai(t *testing.T) {
	const (
		causeQueLeCodeNeConnaitPas = "en cours de lecture par un import en lot"
		promesseQueRienNeTient     = "Réessayez dans un instant"
	)

	for _, affirmation := range []string{causeQueLeCodeNeConnaitPas, promesseQueRienNeTient} {
		if strings.Contains(messageTourTropLoin, affirmation) {
			t.Errorf("le message du renoncement affirme %q, ce que le code ne sait pas : %q", affirmation, messageTourTropLoin)
		}
	}
}

// Le pendant sur POST /recettes : un hôte poli y coûte l'illustration, jamais
// la saisie. C'est la même borne que pour l'hôte occupé, et la recette est
// enregistrée sans image, exactement comme quand l'image est injoignable.
func TestLeCrawlDelayPlusLongQueLaBorneNeCoutePasLaRecette(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	serveur := serveurDImagesRobots(t, robotsQuiDemande(crawlDelayAnnonce), sertLesOctets(pngDeTest(t), "image/png"))
	avecTelechargement(t, autoriseLesServeurs(serveur))

	champs := champsValides()
	champs.Set("image_url", serveur.URL+"/photo.png")
	if rec := poste(t, mux, "/recettes", cookie, champs); rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if image := laRecette(t, app).GetString("image"); image != "" {
		t.Errorf("image %q enregistrée, attendue aucune : la borne n'a pas tenu sur le Crawl-delay de l'hôte", image)
	}
}
