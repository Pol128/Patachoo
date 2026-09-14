package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/logger"
	"github.com/pocketbase/pocketbase/tools/router"
)

const (
	courrielDeTest   = "cuisinier@exemple.fr"
	motDePasseDeTest = "chausson-aux-pommes"
	nomDeTest        = "Cuisinier"
)

// serveurDeTest monte une base neuve, puis le routeur de PocketBase chargé de
// nos middlewares et de nos routes — exactement ce que main() enregistre.
//
// Le montage complet, et non un RequestEvent nu comme dans pages_test.go : ce
// qui est en jeu ici est l'ordre des middlewares. Un montage de complaisance,
// où notre middleware serait appelé à la main, ne prouverait justement pas que
// le cookie est lu avant que pbLoadAuthToken ne cherche l'en-tête.
func serveurDeTest(t *testing.T, routesEnPlus ...func(*router.Router[*core.RequestEvent])) (core.App, http.Handler) {
	t.Helper()
	return monteLeServeur(t, baseNeuveAvec(t, analyseurDeTest(t)), routesEnPlus...)
}

// serveurDeTestAuCoutBcryptReel est le même serveur, sur une base qui garde le
// facteur bcrypt de PocketBase. À ne prendre que si le temps de hachage est
// lui-même le sujet du test — il coûte ~200 ms de plus par compte connecté.
func serveurDeTestAuCoutBcryptReel(t *testing.T, routesEnPlus ...func(*router.Router[*core.RequestEvent])) (core.App, http.Handler) {
	t.Helper()
	return monteLeServeur(t, baseNeuveAuCoutBcryptReel(t, analyseurDeTest(t)), routesEnPlus...)
}

func monteLeServeur(t *testing.T, app core.App, routesEnPlus ...func(*router.Router[*core.RequestEvent])) (core.App, http.Handler) {
	t.Helper()

	routeur, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("routeur : %v", err)
	}
	brancheLesRoutes(routeur)
	for _, ajoute := range routesEnPlus {
		ajoute(routeur)
	}

	mux, err := routeur.BuildMux()
	if err != nil {
		t.Fatalf("mux : %v", err)
	}
	return app, mux
}

// sonde est la route témoin : elle dit qui la chaîne d'authentification a
// reconnu. Aucune page du produit ne le dirait aussi franchement.
func sonde(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/sonde", ditQuiEstReconnu)
}

// sondeSousLAdministration monte la même route témoin sous le préfixe /_/.
//
// Une route à nous, et non celle de PocketBase : apis.NewRouter n'enregistre
// pas /_/{path...}, que PocketBase pose dans son propre OnServe
// (apis/serve.go). Sans route sous ce préfixe, le http.ServeMux rendrait 404
// sans exécuter un seul middleware, et un test de ce qu'y fait la session
// passerait sans rien prouver. Ce qui est en cause est de toute façon notre
// décision sur le chemin, pas ce que PocketBase sert derrière.
func sondeSousLAdministration(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/_/sonde", ditQuiEstReconnu)
}

// ditQuiEstReconnu est le gestionnaire commun aux deux sondes : le montage
// change, la réponse non.
func ditQuiEstReconnu(e *core.RequestEvent) error {
	if e.Auth == nil {
		return e.String(http.StatusOK, "visiteur")
	}
	return e.String(http.StatusOK, e.Auth.Id)
}

// sondeProtegee exige une session par le middleware de PocketBase, et non par
// une vérification à nous : c'est lui que les pages à venir emploieront.
func sondeProtegee(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/sonde-protegee", func(e *core.RequestEvent) error {
		return e.String(http.StatusOK, "ok")
	}).Bind(apis.RequireAuth())
}

// compteParDefaut crée le compte dont les tests de session se servent pour se
// connecter.
//
// Le nom le distingue de compteDeTest (acces_test.go), qui crée un compte nu
// dans une collection quelconque : les deux vivent dans le même paquet, et ne
// posent ni le même mot de passe, ni de nom.
func compteParDefaut(t *testing.T, app core.App) *core.Record {
	t.Helper()
	return creeCompte(t, app, courrielDeTest, nomDeTest)
}

// creeCompte crée un compte quelconque : les tests où deux identités se
// disputent la même requête en demandent un second.
func creeCompte(t *testing.T, app core.App, courriel, nom string) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}

	compte := core.NewRecord(collection)
	compte.SetEmail(courriel)
	compte.SetPassword(motDePasseDeTest)
	compte.Set("name", nom)
	if err := app.Save(compte); err != nil {
		t.Fatalf("création du compte %q : %v", courriel, err)
	}
	return compte
}

// jetonRenouvelableCourt émet un jeton ordinaire — donc renouvelable — mais de
// courte vie, en abaissant le temps de l'émission la durée déclarée sur la
// collection.
//
// NewStaticAuthToken donnerait bien un jeton court, mais non renouvelable :
// c'est celui de la route d'impersonation, et le renouvellement doit justement
// le refuser. Un test du renouvellement bâti sur lui ne couvrirait pas le
// chemin réel, celui d'une session de navigateur.
//
// Seule la durée change : PocketBase ne réémet le secret de signature que sur
// un changement de règle d'authentification (core/collection_model.go), donc le
// jeton reste valable après le rétablissement.
func jetonRenouvelableCourt(t *testing.T, app core.App, compte *core.Record, vie time.Duration) string {
	t.Helper()

	collection := compte.Collection()
	dureeInitiale := collection.AuthToken.Duration

	collection.AuthToken.Duration = int64(vie.Seconds())
	if err := app.Save(collection); err != nil {
		t.Fatalf("abaissement de la durée du jeton : %v", err)
	}

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton court : %v", err)
	}

	collection.AuthToken.Duration = dureeInitiale
	if err := app.Save(collection); err != nil {
		t.Fatalf("rétablissement de la durée du jeton : %v", err)
	}
	return jeton
}

// poseLaRegleDAuthentification écrit la règle qui décide quels comptes ont le
// droit d'ouvrir une session. La collection est livrée sans elle — tout le
// monde passe —, donc un test du refus doit la poser lui-même.
func poseLaRegleDAuthentification(t *testing.T, app core.App, regle string) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}

	collection.AuthRule = &regle
	if err := app.Save(collection); err != nil {
		t.Fatalf("pose de la règle d'authentification : %v", err)
	}
}

// coupeLAuthentificationParMotDePasse ferme l'interrupteur que
// l'administration expose sur la collection.
func coupeLAuthentificationParMotDePasse(t *testing.T, app core.App) {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}

	collection.PasswordAuth.Enabled = false
	if err := app.Save(collection); err != nil {
		t.Fatalf("coupure de l'authentification par mot de passe : %v", err)
	}
}

// ipDeTest est l'adresse que httptest.NewRequest pose d'elle-même sur toute
// requête. La nommer ne change rien à ce que les tests exercent ; elle donne
// seulement un nom à l'IP par défaut, maintenant que le plafond de débit de
// PATA-39 compte par IP et que certains tests doivent en choisir une autre.
const ipDeTest = "192.0.2.1"

// seConnecte poste le formulaire de connexion.
func seConnecte(t *testing.T, mux http.Handler, courriel, motDePasse string) *httptest.ResponseRecorder {
	t.Helper()
	return seConnecteDepuis(t, mux, ipDeTest, courriel, motDePasse)
}

// seConnecteDepuis poste le même formulaire, depuis l'adresse donnée.
//
// RealIP() retombe sur RemoteAddr tant que TrustedProxy.Headers est vide
// (core/event_request.go), donc c'est bien cette adresse-là que le plafond de
// débit compte.
func seConnecteDepuis(t *testing.T, mux http.Handler, ip, courriel, motDePasse string) *httptest.ResponseRecorder {
	t.Helper()

	champs := url.Values{"courriel": {courriel}, "mot-de-passe": {motDePasse}}
	req := httptest.NewRequest(http.MethodPost, "/connexion", strings.NewReader(champs.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = net.JoinHostPort(ip, "1234")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// plusCourtEchecDeConnexion joue plusieurs fois le même échec de connexion et
// rend la plus courte des durées mesurées.
//
// Le minimum, et non la moyenne : c'est le plancher qui dit le travail
// réellement fait, là où une moyenne mêle le coût du hachage aux aléas
// d'ordonnancement de la machine. C'est aussi ce qui tient ce test à l'écart du
// rouge intermittent — le bruit ne fait qu'ajouter du temps, jamais en
// retrancher, donc il ne survit pas au minimum d'un tirage un peu large. Chaque mesure vérifie au passage que la
// connexion a bien échoué — une réussite mesurerait un autre chemin.
//
// Une IP par mesure : le plafond de PATA-39 refuse la sixième tentative d'une
// même adresse sans rien hacher, et ce refus-là — quelques microsecondes —
// deviendrait le minimum du tirage. Le test mesurerait alors le limiteur, pas
// le hachage. Les deux courriels rejouent la même série d'adresses, donc dans
// les mêmes conditions.
func plusCourtEchecDeConnexion(t *testing.T, mux http.Handler, courriel string, mesures int) time.Duration {
	t.Helper()

	var plusCourt time.Duration
	for i := 0; i < mesures; i++ {
		debut := time.Now()
		rec := seConnecteDepuis(t, mux, fmt.Sprintf("198.51.100.%d", i+1), courriel, "pas-le-bon-mot-de-passe")
		ecoule := time.Since(debut)

		if cookie := cookieEventuelDe(rec); cookie != nil {
			t.Fatalf("la connexion de %q a réussi : la mesure ne porte pas sur un échec", courriel)
		}
		if i == 0 || ecoule < plusCourt {
			plusCourt = ecoule
		}
	}
	return plusCourt
}

// avecCookie joue une requête portant ce seul cookie, sans en-tête
// Authorization : c'est ce que fait un navigateur qui demande une page.
func avecCookie(mux http.Handler, methode, cible string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(methode, cible, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// cookieDe extrait le cookie de session d'une réponse, ou fait échouer le test.
func cookieDe(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()

	cookie := cookieEventuelDe(rec)
	if cookie == nil {
		t.Fatalf("aucun cookie %q dans la réponse : %q", nomCookieSession, rec.Header().Values("Set-Cookie"))
	}
	return cookie
}

// cookieEventuelDe rend le premier cookie de session, ou nil s'il n'y en a pas.
func cookieEventuelDe(rec *httptest.ResponseRecorder) *http.Cookie {
	poses := cookiesDeSession(rec)
	if len(poses) == 0 {
		return nil
	}
	return poses[0]
}

// cookiesDeSession rend tous les Set-Cookie de session de la réponse.
//
// Leur nombre est ce qui compte : http.SetCookie ajoute un en-tête au lieu de
// le remplacer, donc deux étapes qui posent chacune le leur partent ensemble,
// et la réponse ne vaut plus que par leur ordre.
func cookiesDeSession(rec *httptest.ResponseRecorder) []*http.Cookie {
	var poses []*http.Cookie
	for _, cookie := range (&http.Response{Header: rec.Header()}).Cookies() {
		if cookie.Name == nomCookieSession {
			poses = append(poses, cookie)
		}
	}
	return poses
}

// avecEnTete joue une requête authentifiée par le seul en-tête Authorization,
// et un cookie facultatif : c'est ce que fait un client d'API, à qui le
// navigateur ne dicte rien.
func avecEnTete(mux http.Handler, methode, cible, jeton string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(methode, cible, nil)
	req.Header.Set("Authorization", jeton)
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// attributsDeSession vérifie les quatre attributs qui ne changent jamais,
// que le cookie soit déposé, renouvelé ou effacé.
func attributsDeSession(t *testing.T, cookie *http.Cookie) {
	t.Helper()

	if !cookie.HttpOnly {
		t.Error("cookie sans HttpOnly : le jeton serait lisible en JavaScript")
	}
	if !cookie.Secure {
		t.Error("cookie sans Secure")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite %v, attendu Lax : c'est notre seule défense CSRF", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path %q, attendu %q", cookie.Path, "/")
	}
}

// journaux rend tout ce que le journal a écrit, ou "" au bout de deux
// secondes.
//
// PocketBase journalise chaque requête dans une goroutine détachée
// (routine.FireAndForget), puis accumule les lignes dans un tampon : lire une
// seule fois, tout de suite, rendrait un journal vide et un test qui ne prouve
// rien. D'où l'attente — bornée, et qui se termine dès la première ligne.
func journaux(t *testing.T, app core.App) string {
	t.Helper()

	limite := time.Now().Add(2 * time.Second)
	for {
		ecrit := journalEcrit(t, app)
		if ecrit != "" || time.Now().After(limite) {
			return ecrit
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// journalEcrit vide le tampon du journal et rend ce qui est en base.
func journalEcrit(t *testing.T, app core.App) string {
	t.Helper()

	if tampon, ok := app.Logger().Handler().(*logger.BatchHandler); ok {
		if err := tampon.WriteAll(context.Background()); err != nil {
			t.Fatalf("vidage du journal : %v", err)
		}
	}

	lignes := []*core.Log{}
	if err := app.LogQuery().All(&lignes); err != nil {
		t.Fatalf("lecture du journal : %v", err)
	}

	var tout strings.Builder
	for _, ligne := range lignes {
		tout.WriteString(ligne.Message)
		donnees, _ := json.Marshal(ligne.Data)
		tout.Write(donnees)
		tout.WriteByte('\n')
	}
	return tout.String()
}

// --- Le dépôt du cookie ---------------------------------------------------

func TestUneConnexionReussieDeposeUnCookieDeSession(t *testing.T) {
	app, mux := serveurDeTest(t)
	compte := compteParDefaut(t, app)

	rec := seConnecte(t, mux, courrielDeTest, motDePasseDeTest)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	cookie := cookieDe(t, rec)
	if cookie.Value == "" {
		t.Fatal("cookie de session vide")
	}
	attributsDeSession(t, cookie)

	// Le Max-Age se lit sur la collection, il n'est pas recopié en dur : un
	// cookie qui survit à son jeton produit une session fantôme, et l'inverse
	// une déconnexion inexpliquée.
	duree := int(compte.Collection().AuthToken.Duration)
	if cookie.MaxAge != duree {
		t.Errorf("Max-Age %d, attendu %d — la durée de vie du jeton", cookie.MaxAge, duree)
	}
}

// Le critère qui commande tout le reste : PocketBase ne lit le jeton que dans
// l'en-tête Authorization, qu'un navigateur n'envoie pas. Sans notre
// middleware, la connexion ne survivrait pas au rechargement.
func TestLeCookieSeulAuthentifieLaRequeteSuivante(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodGet, "/sonde", cookie)

	if rec.Body.String() != compte.Id {
		t.Errorf("la sonde a reconnu %q, attendu %q", rec.Body.String(), compte.Id)
	}
}

func TestUnCookieInexploitableLaisseLaRequeteEnVisiteur(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteParDefaut(t, app)

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton : %v", err)
	}
	// Le jeton vient d'être signé avec la clé du compte ; en la renouvelant,
	// sa signature ne vaut plus rien. C'est le cas « signé avec un autre
	// secret », sans avoir à forger un JWT à la main.
	compte.RefreshTokenKey()
	if err := app.Save(compte); err != nil {
		t.Fatalf("renouvellement de la clé : %v", err)
	}

	cas := []struct {
		nom    string
		cookie *http.Cookie
	}{
		{"absent", nil},
		{"vide", &http.Cookie{Name: nomCookieSession, Value: ""}},
		{"tronqué", &http.Cookie{Name: nomCookieSession, Value: jeton[:len(jeton)/2]}},
		{"signé avec un autre secret", &http.Cookie{Name: nomCookieSession, Value: jeton}},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			rec := avecCookie(mux, http.MethodGet, "/sonde", c.cookie)

			if rec.Code != http.StatusOK {
				t.Errorf("statut %d, attendu %d : la requête doit se poursuivre en visiteur", rec.Code, http.StatusOK)
			}
			if rec.Body.String() != "visiteur" {
				t.Errorf("la sonde a reconnu %q, attendu un visiteur", rec.Body.String())
			}
		})
	}
}

// --- Le cookie et l'en-tête face à face ------------------------------------

// Deux identités dans la même requête, et une seule doit gagner : celle que
// le client porte lui-même. Un cookie que le navigateur joint d'office ne
// supplante pas le jeton d'un client d'API.
func TestLEnTeteAuthorizationLEmporteSurLeCookie(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	porteur := compteParDefaut(t, app)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	jeton, err := autre.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton : %v", err)
	}

	rec := avecEnTete(mux, http.MethodGet, "/sonde", jeton, cookie)

	if rec.Body.String() != autre.Id {
		t.Errorf("la sonde a reconnu %q, attendu %q — le cookie de %q a supplanté l'en-tête",
			rec.Body.String(), autre.Id, porteur.Id)
	}
}

// Un client d'API n'a pas demandé de cookie : lui en poser un de cinq jours,
// HttpOnly et Path=/, serait lui imposer une session qu'il ne gère pas.
func TestUneSessionPorteeParLEnTeteNestPasRenouvelee(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteParDefaut(t, app)

	// Renouvelable, et non statique : c'est la garde du cookie que ce test
	// exerce, et un jeton que le renouvellement refuserait de toute façon la
	// laisserait passer sans rien prouver.
	court := jetonRenouvelableCourt(t, app, compte, time.Minute)

	rec := avecEnTete(mux, http.MethodGet, "/sonde", court, nil)

	if rec.Body.String() != compte.Id {
		t.Fatalf("la sonde a reconnu %q, attendu %q", rec.Body.String(), compte.Id)
	}
	if pose := cookieEventuelDe(rec); pose != nil {
		t.Errorf("un cookie de session a été posé à un client d'API : %q", pose.Value)
	}
}

// La variante que le garde « un cookie est présent » ne couvre pas : le cookie
// est bien là, mais ce n'est pas lui qui a authentifié la requête.
//
// Le renouvellement décide sur le jeton du cookie et réémet pour e.Auth. Quand
// les deux ne désignent pas le même compte — cookie de A, en-tête Authorization
// de B —, la recopie ne supplante rien, e.Auth est B, et la réponse reposerait
// la session du navigateur sur un jeton de B, Path=/ et cinq jours. Le cookie
// de A aurait suffi à faire déposer celui d'un autre.
func TestUnCookieNeFaitPasRenouvelerLaSessionDeLEnTete(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	porteur := compteParDefaut(t, app)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")

	// Sous la mi-vie, et renouvelable : tout ce que le renouvellement demande
	// d'un cookie, pour qu'il ne reste plus que l'identité en cause.
	court := jetonRenouvelableCourt(t, app, porteur, time.Minute)
	jetonDeLAutre, err := autre.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton : %v", err)
	}

	rec := avecEnTete(mux, http.MethodGet, "/sonde", jetonDeLAutre,
		&http.Cookie{Name: nomCookieSession, Value: court})

	if rec.Body.String() != autre.Id {
		t.Fatalf("la sonde a reconnu %q, attendu %q", rec.Body.String(), autre.Id)
	}
	if pose := cookieEventuelDe(rec); pose != nil {
		t.Errorf("la réponse repose la session du navigateur sur un jeton qui n'est pas celui du cookie : le compte %q y a gagné la session de %q",
			porteur.Id, autre.Id)
	}
}

// --- La recopie s'arrête aux chemins du produit ----------------------------

// Le cookie est HttpOnly, mais une XSS n'a pas besoin de le lire : un
// fetch("/api/…", {credentials:"same-origin"}) depuis une page de l'instance
// partait authentifié, parce que c'est le serveur qui faisait la recopie. Toute
// XSS obtenait ainsi l'API REST complète du compte, là où elle n'avait que les
// formulaires du produit.
//
// 404 et non 403 : la règle de vue des recettes est « @request.auth.id != "" »,
// et PocketBase répond à un enregistrement que la règle ne couvre pas comme à
// un enregistrement absent (apis/record_crud.go).
func TestLeCookieSeulNouvrePasLaLectureDUneRecetteParLAPI(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodGet, "/api/collections/recipes/records/"+recette.Id, cookie)

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d, attendu %d : le cookie a authentifié la lecture de la recette par l'API",
			rec.Code, http.StatusNotFound)
	}
	if strings.Contains(rec.Body.String(), "Tarte aux pommes") {
		t.Errorf("la réponse porte l'enregistrement :\n%s", rec.Body.String())
	}
}

// Les opérations de compte sont l'autre moitié de ce que la recopie ouvrait :
// auth-refresh prolonge la session, et la même porte mène au changement de
// courriel.
func TestLeCookieSeulNouvrePasLeRenouvellementDeJetonDeLAPI(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodPost, "/api/collections/users/auth-refresh", cookie)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("statut %d, attendu %d : le cookie a authentifié le renouvellement de jeton de l'API",
			rec.Code, http.StatusUnauthorized)
	}
}

// L'autre sens, celui que la correction ne doit pas emporter : l'API reste
// ouverte à qui porte son propre jeton. Le cookie n'était qu'une commodité pour
// le navigateur, son retrait ne retire rien à un client d'API — y compris à un
// client qui traînerait un cookie, cas d'un outil lancé depuis un navigateur
// connecté.
func TestUnClientDAPIEstServiSurLAPIAvecOuSansCookie(t *testing.T) {
	app, mux := serveurDeTest(t)
	compte := compteParDefaut(t, app)
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton : %v", err)
	}
	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	cible := "/api/collections/recipes/records/" + recette.Id

	for _, cas := range []struct {
		nom    string
		cookie *http.Cookie
	}{
		{"sans cookie", nil},
		{"avec cookie", cookie},
	} {
		t.Run(cas.nom, func(t *testing.T) {
			rec := avecEnTete(mux, http.MethodGet, cible, jeton, cas.cookie)

			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d, attendu %d :\n%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "Tarte aux pommes") {
				t.Errorf("la réponse ne porte pas l'enregistrement :\n%s", rec.Body.String())
			}
		})
	}
}

// La seule adresse /api/ que le navigateur demande, donc la seule régression
// visible possible. Elle tient parce que le champ image est un FileField sans
// Protected (migrations/1787093600_schema_initial.go) : fileApi.download
// n'exige un jeton que sur un champ protégé, et la vignette est servie sans
// authentification — avec ou sans cookie.
//
// L'adresse est celle que le produit fabrique lui-même, et non une chaîne
// recopiée : c'est bien celle-là que la page met dans son <img>.
func TestLaVignetteResteServieAvecLeCookieSeul(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes", avecImage: true})

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodGet, urlDeLaMiniature(recette), cookie)

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d : la vignette d'une recette illustrée n'est plus servie\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
}

// Le marqueur compte autant que l'en-tête. Laissé posé sur une requête d'API,
// il ferait reposer par renouvelleLaSession un cookie de session de cinq jours
// sur une réponse de l'API REST — un navigateur qui n'a rien demandé y gagnerait
// une session fraîche, et la réponse d'une API porterait un Set-Cookie.
//
// Les deux préfixes, parce que la garde les nomme tous les deux : /_/ y figure
// alors qu'un jeton users n'y donne rien, la règle étant sur le chemin et non
// sur ce que le chemin sert.
func TestAucuneRequeteDePocketBaseNeReposeLeCookieDeSession(t *testing.T) {
	app, mux := serveurDeTest(t, sonde, sondeSousLAdministration)
	compte := compteParDefaut(t, app)
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	// Sous la mi-vie et renouvelable : tout ce que le renouvellement demande,
	// pour qu'il ne reste plus que le chemin en cause.
	court := jetonRenouvelableCourt(t, app, compte, time.Minute)
	cookie := &http.Cookie{Name: nomCookieSession, Value: court}

	// Le témoin. Sans lui, ce test passerait aussi sur un jeton que le
	// renouvellement aurait refusé de toute façon, et ne prouverait rien.
	if pose := cookieEventuelDe(avecCookie(mux, http.MethodGet, "/sonde", cookie)); pose == nil {
		t.Fatalf("le jeton du témoin n'est pas renouvelé sur une page du produit : ce test ne prouve rien")
	}

	for _, cas := range []struct{ nom, cible string }{
		{"l'API REST", "/api/collections/recipes/records/" + recette.Id},
		{"l'interface d'administration", "/_/sonde"},
	} {
		t.Run(cas.nom, func(t *testing.T) {
			rec := avecCookie(mux, http.MethodGet, cas.cible, cookie)

			if poses := cookiesDeSession(rec); len(poses) != 0 {
				t.Errorf("%d cookie(s) %q dans la réponse de %s, attendu 0 : %q",
					len(poses), nomCookieSession, cas.cible, rec.Header().Values("Set-Cookie"))
			}
		})
	}
}

// La variante que le cas du cookie seul ne couvre pas, et c'est le marqueur
// qu'elle garde. Une requête d'API peut porter les deux : son propre en-tête
// Authorization — compte B — et, le navigateur aidant, le cookie de session du
// compte A. L'en-tête n'est alors pas récrit, puisqu'il est déjà là, et le
// cookie seul ne prouve donc rien : seul le marqueur dirait encore que le
// cookie a authentifié la requête. Posé, il ferait décider le renouvellement
// sur le jeton de A et réémettre pour B — la réponse d'un appel d'API
// reposerait la session du navigateur, sur un compte que son cookie ne
// désignait pas.
//
// Ce test est né de la passe de sabotages de la DoD : une garde qui filtrerait
// l'en-tête sans filtrer le marqueur passait tous les autres.
func TestUneRequeteDAPIQuiPorteSonEnTeteNeReposePasLeCookie(t *testing.T) {
	app, mux := serveurDeTest(t)
	porteur := compteParDefaut(t, app)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	recette := creeRecette(t, app, recetteVoulue{titre: "Tarte aux pommes"})

	// Sous la mi-vie et renouvelable : tout ce que le renouvellement demande
	// du jeton que le marqueur porterait.
	court := jetonRenouvelableCourt(t, app, porteur, time.Minute)
	jetonDeLAutre, err := autre.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton : %v", err)
	}

	rec := avecEnTete(mux, http.MethodGet, "/api/collections/recipes/records/"+recette.Id,
		jetonDeLAutre, &http.Cookie{Name: nomCookieSession, Value: court})

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d : le client d'API doit rester servi, c'est le cookie qui est en cause\n%s",
			rec.Code, http.StatusOK, rec.Body.String())
	}
	if poses := cookiesDeSession(rec); len(poses) != 0 {
		t.Errorf("la réponse d'un appel d'API repose la session du navigateur : le compte %q y gagnerait celle de %q — %q",
			porteur.Id, autre.Id, rec.Header().Values("Set-Cookie"))
	}
}

// --- La page de connexion -------------------------------------------------

func TestLaPageDeConnexionPorteLeFormulaire(t *testing.T) {
	_, mux := serveurDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/connexion", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	corps := rec.Body.String()
	for _, attendu := range []string{
		`method="post"`, `action="/connexion"`,
		`name="courriel"`, `name="mot-de-passe"`, `type="password"`,
	} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("formulaire sans %q :\n%s", attendu, corps)
		}
	}
}

func TestLaPageDeConnexionRedirigeUnCompteDejaConnecte(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodGet, "/connexion", cookie)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if lieu := rec.Header().Get("Location"); lieu != "/" {
		t.Errorf("redirigé vers %q, attendu %q", lieu, "/")
	}
}

// Un courriel inconnu et un mot de passe faux se répondent à l'identique :
// sinon la page de connexion devient un annuaire des comptes existants.
func TestUnEchecDeConnexionNeDitPasQuelsCourrielsExistent(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	inconnu := seConnecte(t, mux, "personne@exemple.fr", motDePasseDeTest)
	mauvais := seConnecte(t, mux, courrielDeTest, "pas-le-bon-mot-de-passe")

	if inconnu.Code != mauvais.Code {
		t.Errorf("statuts %d et %d : la page distingue les deux échecs", inconnu.Code, mauvais.Code)
	}
	if inconnu.Body.String() != mauvais.Body.String() {
		t.Errorf("les deux échecs ne rendent pas la même page :\n--- courriel inconnu ---\n%s\n--- mot de passe faux ---\n%s",
			inconnu.Body.String(), mauvais.Body.String())
	}
	if !strings.Contains(mauvais.Body.String(), `role="alert"`) {
		t.Errorf("échec sans message annoncé :\n%s", mauvais.Body.String())
	}
	if cookie := cookieEventuelDe(mauvais); cookie != nil {
		t.Errorf("un échec a déposé un cookie de session : %q", cookie.Value)
	}
}

// Le message est le même, mais le temps de réponse, lui, parlait : un courriel
// inconnu sortait avant tout hachage quand un courriel connu payait le bcrypt
// complet. L'écart se mesure sur le réseau, et fait de la page de connexion un
// annuaire des comptes existants par un autre canal que son message.
//
// La parade est celle que PocketBase pose sur sa propre route
// (apis/record_auth_with_password.go, dummyPasswordCheck) : hacher quand même,
// sur un enregistrement quelconque de la collection.
func TestUnEchecDeConnexionNeDitPasParLeTempsQuelsCourrielsExistent(t *testing.T) {
	const mesures = 9

	// Au coût réel, et c'est tout le sujet : la fixture brade le facteur bcrypt
	// pour les 443 autres tests du paquet, or c'est justement le temps de ce
	// hachage que ce test-ci compare. Sous un facteur bradé, son garde-fou des
	// 5 ms ci-dessous refuserait de conclure — ce qu'il doit faire.
	app, mux := serveurDeTestAuCoutBcryptReel(t)
	compteParDefaut(t, app)

	connu := plusCourtEchecDeConnexion(t, mux, courrielDeTest, mesures)
	inconnu := plusCourtEchecDeConnexion(t, mux, "personne@exemple.fr", mesures)

	// Le hachage domine tout le reste d'un ordre de grandeur. Si le refus de
	// référence ne coûte rien, c'est la mesure qui est fausse, pas le code : le
	// test s'arrête plutôt que de conclure.
	if connu < 5*time.Millisecond {
		t.Fatalf("un refus sur courriel connu ne coûte que %s : la mesure ne prouverait rien", connu)
	}

	// Le quart, et non la moitié. C'est un test de temps mural, donc exposé au
	// rouge intermittent, et la marge se choisit en regardant les deux échelles
	// en présence : le défaut qu'il attrape est d'un facteur 600 — 160 µs
	// contre 96 ms à la mesure d'origine — quand le bruit d'ordonnancement qui
	// survit au minimum de neuf tirages se compte en pour-cent. Entre les deux,
	// n'importe quel seuil convient ; le plus large des deux est celui qui ne
	// rougira pas sur une machine chargée.
	if inconnu < connu/4 {
		t.Errorf("refus en %s sur courriel inconnu contre %s sur courriel connu : le temps de réponse dit quels comptes existent",
			inconnu, connu)
	}
}

// La règle d'authentification de la collection décide quels comptes ont le
// droit d'ouvrir une session : un compte non vérifié, suspendu, restreint. Elle
// se pose dans l'administration, et PocketBase la lit sur sa propre route
// (apis/record_helpers.go, recordAuthResponse) — pas au chargement du jeton.
// Une page de connexion qui ne la lit pas la laisse échouer en silence, du bon
// côté pour l'attaquant, et le jeton qu'elle émet reste valable cinq jours.
func TestUneConnexionEstRefuseeParLaRegleDAuthentification(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	poseLaRegleDAuthentification(t, app, "verified = true")

	rec := seConnecte(t, mux, courrielDeTest, motDePasseDeTest)

	if cookie := cookieEventuelDe(rec); cookie != nil {
		t.Fatalf("une session a été ouverte pour un compte que la règle refuse : %q", cookie.Value)
	}
	if !strings.Contains(rec.Body.String(), messageEchecConnexion) {
		t.Errorf("le refus n'annonce pas l'échec :\n%s", rec.Body.String())
	}
}

// L'interrupteur que l'administration expose doit couper quelque chose. Sans
// cette lecture, il ne coupe que la route de PocketBase, et la nôtre continue
// d'authentifier par mot de passe.
func TestUneConnexionEstRefuseeQuandLeMotDePasseEstCoupe(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)
	coupeLAuthentificationParMotDePasse(t, app)

	rec := seConnecte(t, mux, courrielDeTest, motDePasseDeTest)

	if cookie := cookieEventuelDe(rec); cookie != nil {
		t.Fatalf("une session a été ouverte alors que le mot de passe est coupé : %q", cookie.Value)
	}
	if !strings.Contains(rec.Body.String(), messageEchecConnexion) {
		t.Errorf("le refus n'annonce pas l'échec :\n%s", rec.Body.String())
	}
}

func TestLeMotDePasseNapparaitNiDansLaPageNiDansLesJournaux(t *testing.T) {
	const saisi = "sirop-de-liege-mal-tape"

	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	rec := seConnecte(t, mux, courrielDeTest, saisi)

	if strings.Contains(rec.Body.String(), saisi) {
		t.Errorf("le mot de passe est réaffiché dans la page :\n%s", rec.Body.String())
	}

	journal := journaux(t, app)
	if journal == "" {
		t.Fatal("journal vide : le test ne prouverait rien")
	}
	if strings.Contains(journal, saisi) {
		t.Errorf("le mot de passe est dans les journaux :\n%s", journal)
	}
}

// --- La déconnexion -------------------------------------------------------

func TestLaDeconnexionEffaceLeCookie(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodPost, "/deconnexion", cookie)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	efface := cookieDe(t, rec)
	if efface.Value != "" {
		t.Errorf("cookie d'effacement de valeur %q, attendue vide", efface.Value)
	}
	if efface.MaxAge >= 0 {
		t.Errorf("Max-Age %d, attendu négatif", efface.MaxAge)
	}
	// Mêmes attributs, sinon le navigateur garde le cookie d'origine à côté
	// de celui qu'on croit avoir effacé.
	attributsDeSession(t, efface)

	// Ce que le navigateur enverra ensuite, c'est ce cookie-là : vide.
	suivante := avecCookie(mux, http.MethodGet, "/sonde", efface)
	if suivante.Body.String() != "visiteur" {
		t.Errorf("la sonde a reconnu %q après déconnexion, attendu un visiteur", suivante.Body.String())
	}
}

// Le renouvellement passe avant le gestionnaire : sous la mi-vie, il pose un
// jeton frais de cinq jours dans la réponse même qui est censée révoquer la
// session. Deux Set-Cookie de même nom partent alors ensemble, et la
// déconnexion ne vaut plus que par leur ordre.
func TestLaDeconnexionSousLaMiVieNeRenvoieQueLEffacement(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteParDefaut(t, app)

	// Renouvelable : c'est la rencontre du renouvellement et de l'effacement
	// que ce test garde, et elle n'a lieu que si le premier se déclenche.
	court := jetonRenouvelableCourt(t, app, compte, time.Minute)

	rec := avecCookie(mux, http.MethodPost, "/deconnexion", &http.Cookie{Name: nomCookieSession, Value: court})

	poses := cookiesDeSession(rec)
	if len(poses) != 1 {
		t.Fatalf("%d cookies %q dans la réponse, attendu 1 : %q",
			len(poses), nomCookieSession, rec.Header().Values("Set-Cookie"))
	}
	if poses[0].Value != "" {
		t.Errorf("cookie de valeur %q, attendue vide", poses[0].Value)
	}
	if poses[0].MaxAge >= 0 {
		t.Errorf("Max-Age %d, attendu négatif", poses[0].MaxAge)
	}
	attributsDeSession(t, poses[0])
}

// --- Les priorités de middleware ------------------------------------------

// Nos deux priorités ne doivent heurter aucune de celles que PocketBase
// déclare. À égalité, le tri des handlers préserve l'ordre d'enregistrement
// (tools/hook/hook.go:98, « sort handlers by Priority, preserving the original
// order of equal items ») : deux middlewares de même priorité s'exécutent alors
// dans un ordre qui ne se lit plus nulle part, et qu'un changement d'ordre de
// branchement suffit à retourner.
//
// L'énumération est écrite à la main, et c'est voulu : PocketBase n'expose
// aucune liste de ses priorités, donc une constante ajoutée en amont ne se
// signalera pas toute seule. Ce test dit ce qui était vrai à la lecture de la
// v0.39.11 ; une montée de version qui en ajoute une demande d'y revenir.
func TestLesPrioritesDeSessionNeHeurtentAucuneDePocketBase(t *testing.T) {
	dePocketBase := map[string]int{
		"CORS":                    apis.DefaultCorsMiddlewarePriority,
		"journal d'activité":      apis.DefaultActivityLoggerMiddlewarePriority,
		"recouvrement de panique": apis.DefaultPanicRecoverMiddlewarePriority,
		"chargement du jeton":     apis.DefaultLoadAuthTokenMiddlewarePriority,
		"liste blanche superuser": apis.DefaultSuperuserIPsWhitelistMiddlewarePriority,
		"en-têtes de sécurité":    apis.DefaultSecurityHeadersMiddlewarePriority,
		"limitation de débit":     apis.DefaultRateLimitMiddlewarePriority,
		"taille du corps":         apis.DefaultBodyLimitMiddlewarePriority,
		"redirection www":         apis.DefaultWWWRedirectMiddlewarePriority,
	}

	lesNotres := map[string]int{
		"recopie du cookie": prioriteRecopieDuCookie,
		"renouvellement":    prioriteRenouvellement,
	}

	for nom, priorite := range lesNotres {
		for nomPB, prioritePB := range dePocketBase {
			if priorite == prioritePB {
				t.Errorf("le middleware de %s (priorité %d) est à égalité avec celui de PocketBase pour %s : leur ordre ne tient plus qu'à celui du branchement",
					nom, priorite, nomPB)
			}
		}
	}
}

// Les deux encadrements que les priorités doivent produire, dits en toutes
// lettres : sans eux, un écart choisi pour éviter une collision pourrait faire
// franchir à l'un des deux middlewares la borne qui justifie son existence.
func TestLesPrioritesDeSessionEncadrentLeChargementDuJeton(t *testing.T) {
	if prioriteRecopieDuCookie >= apis.DefaultLoadAuthTokenMiddlewarePriority {
		t.Errorf("la recopie du cookie (%d) doit passer avant le chargement du jeton (%d), qui ne lit que l'en-tête",
			prioriteRecopieDuCookie, apis.DefaultLoadAuthTokenMiddlewarePriority)
	}
	if prioriteRenouvellement <= apis.DefaultLoadAuthTokenMiddlewarePriority {
		t.Errorf("le renouvellement (%d) doit passer après le chargement du jeton (%d), qui peuple e.Auth",
			prioriteRenouvellement, apis.DefaultLoadAuthTokenMiddlewarePriority)
	}
	// Une panique dans la recopie du cookie doit rester rattrapable : passer
	// avant le recouvrement la ferait remonter jusqu'au serveur nu.
	if prioriteRecopieDuCookie <= apis.DefaultPanicRecoverMiddlewarePriority {
		t.Errorf("la recopie du cookie (%d) doit passer après le recouvrement de panique (%d)",
			prioriteRecopieDuCookie, apis.DefaultPanicRecoverMiddlewarePriority)
	}
}

// --- Le renouvellement ----------------------------------------------------

func TestUnJetonFraisNestPasRenouvele(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodGet, "/sonde", cookie)

	if redepose := cookieEventuelDe(rec); redepose != nil {
		t.Errorf("un jeton frais a été renouvelé : %q", redepose.Value)
	}
}

// Sans renouvellement, toute session meurt sèchement au bout de cinq jours,
// y compris en pleine saisie.
func TestUnJetonSousLaMiVieEstRenouvele(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteParDefaut(t, app)

	// La durée réduite que demande le critère, obtenue sans changer la nature
	// du jeton : celui-ci est ordinaire, donc renouvelable, et c'est le chemin
	// d'une session de navigateur — le seul que le renouvellement serve.
	court := jetonRenouvelableCourt(t, app, compte, time.Minute)

	rec := avecCookie(mux, http.MethodGet, "/sonde", &http.Cookie{Name: nomCookieSession, Value: court})

	if rec.Body.String() != compte.Id {
		t.Fatalf("la sonde a reconnu %q, attendu %q", rec.Body.String(), compte.Id)
	}
	frais := cookieDe(t, rec)
	if frais.Value == court {
		t.Error("le cookie a été redéposé avec le même jeton")
	}
	if attendu := int(compte.Collection().AuthToken.Duration); frais.MaxAge != attendu {
		t.Errorf("Max-Age %d, attendu %d", frais.MaxAge, attendu)
	}
	attributsDeSession(t, frais)
}

// Un jeton statique porte une borne choisie à l'émission — c'est tout le sens
// du paramètre de durée de NewStaticAuthToken, qu'emploie la route
// d'impersonation. Le remplacer par un jeton ordinaire de cinq jours effacerait
// cette borne, et la session se prolongerait ensuite de renouvellement en
// renouvellement.
//
// PocketBase pose la même règle sur sa propre route de renouvellement
// (apis/record_auth_refresh.go) : pas de revendication refreshable, pas de
// réémission.
func TestUnJetonNonRenouvelableNestPasRenouvele(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteParDefaut(t, app)

	statique, err := compte.NewStaticAuthToken(30 * time.Second)
	if err != nil {
		t.Fatalf("émission du jeton statique : %v", err)
	}

	rec := avecCookie(mux, http.MethodGet, "/sonde", &http.Cookie{Name: nomCookieSession, Value: statique})

	if rec.Body.String() != compte.Id {
		t.Fatalf("la sonde a reconnu %q, attendu %q : un jeton statique reste valable, il ne se renouvelle simplement pas",
			rec.Body.String(), compte.Id)
	}
	if pose := cookieEventuelDe(rec); pose != nil {
		t.Errorf("un jeton non renouvelable a été renouvelé : %q", pose.Value)
	}
}

// Une session ouverte ne prolonge pas un droit que l'administration a retiré.
//
// La règle d'authentification décide de la prolongation comme elle décide de
// l'ouverture. Sans cette lecture, un compte que la règle ne couvre plus —
// resserrée, ou compte repassé non vérifié — garde sa session indéfiniment
// tant qu'il émet une requête par demi-vie : il ne se reconnecte jamais, donc
// le contrôle posé sur la route de connexion ne l'atteint plus, et chaque
// renouvellement repousse l'échéance d'autant.
//
// L'ordre des trois premières lignes est ce que ce test a de délicat. Poser la
// règle après l'émission du jeton ferait tourner le secret de signature de la
// collection — « invalidate previously issued auth tokens on auth rule
// change », core/collection_model.go : le cookie ne serait plus lisible, la
// requête repartirait en visiteur, et l'absence de Set-Cookie ne prouverait
// rien. La règle est donc posée d'abord, le compte relu ensuite pour que sa
// collection la porte, et la sonde vérifie que le jeton authentifie toujours —
// sans quoi ce test serait vert pour la mauvaise raison.
func TestUneSessionQueLaRegleNautorisePlusNestPasRenouvelee(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compteParDefaut(t, app)
	poseLaRegleDAuthentification(t, app, "verified = true")

	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("relecture du compte : %v", err)
	}
	court := jetonRenouvelableCourt(t, app, compte, time.Minute)

	rec := avecCookie(mux, http.MethodGet, "/sonde", &http.Cookie{Name: nomCookieSession, Value: court})

	if rec.Body.String() != compte.Id {
		t.Fatalf("la sonde a reconnu %q, attendu %q : le jeton doit rester valable, c'est sa prolongation qui est en cause",
			rec.Body.String(), compte.Id)
	}
	if pose := cookieEventuelDe(rec); pose != nil {
		t.Errorf("la session d'un compte que la règle refuse a été prolongée : %q", pose.Value)
	}
}

// --- Les règles d'accès ---------------------------------------------------

func TestUneRouteProtegeeExigeLaSession(t *testing.T) {
	app, mux := serveurDeTest(t, sondeProtegee)
	compteParDefaut(t, app)

	sans := avecCookie(mux, http.MethodGet, "/sonde-protegee", nil)
	if sans.Code != http.StatusUnauthorized {
		t.Errorf("sans cookie : statut %d, attendu %d", sans.Code, http.StatusUnauthorized)
	}

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	avec := avecCookie(mux, http.MethodGet, "/sonde-protegee", cookie)
	if avec.Code != http.StatusOK {
		t.Errorf("avec cookie : statut %d, attendu %d", avec.Code, http.StatusOK)
	}
}

// --- L'utilisateur courant dans les gabarits ------------------------------

func TestLEnTetePorteLeCompteConnecte(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	corps := avecCookie(mux, http.MethodGet, "/recettes", cookie).Body.String()

	if !strings.Contains(corps, nomDeTest) {
		t.Errorf("en-tête sans le nom du compte connecté :\n%s", corps)
	}
	if !strings.Contains(corps, `action="/deconnexion"`) {
		t.Errorf("en-tête sans lien de déconnexion :\n%s", corps)
	}
	if strings.Contains(corps, `href="/connexion"`) {
		t.Errorf("un compte connecté se voit proposer de se connecter :\n%s", corps)
	}
}

// DOD.md §3 : le nom d'un compte ressort dans l'en-tête de chaque page, et
// rien ne dit qu'il a été saisi de bonne foi. Le test porte sur le HTML rendu,
// pas sur un appel d'échappement.
func TestLEnTeteEchappeLeNomDuCompte(t *testing.T) {
	app, mux := serveurDeTest(t)

	compte := compteParDefaut(t, app)
	compte.Set("name", `<script>alert(1)</script>`)
	if err := app.Save(compte); err != nil {
		t.Fatalf("renommage du compte : %v", err)
	}

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	corps := avecCookie(mux, http.MethodGet, "/recettes", cookie).Body.String()

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("nom de compte non échappé :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("nom de compte absent de l'en-tête :\n%s", corps)
	}
}

// Le champ name n'est pas requis sur la collection users : un compte peut
// exister sans nom, et l'en-tête doit malgré tout désigner qui est connecté. Il
// retombe alors sur le courriel — retirer ce repli laisse l'en-tête annoncer
// « Connecté en tant que » suivi de rien.
func TestLEnTeteRetombeSurLeCourrielQuandLeCompteNaPasDeNom(t *testing.T) {
	const courrielSansNom = "sans-nom@exemple.fr"

	app, mux := serveurDeTest(t)
	creeCompte(t, app, courrielSansNom, "")

	cookie := cookieDe(t, seConnecte(t, mux, courrielSansNom, motDePasseDeTest))
	corps := avecCookie(mux, http.MethodGet, "/recettes", cookie).Body.String()

	if !strings.Contains(corps, courrielSansNom) {
		t.Errorf("en-tête sans le courriel du compte dépourvu de nom :\n%s", corps)
	}
	if !strings.Contains(corps, `action="/deconnexion"`) {
		t.Errorf("en-tête sans lien de déconnexion : le compte n'est pas reconnu :\n%s", corps)
	}
}

// La page de connexion, et non la liste : celle-ci est derrière la session, et
// un visiteur n'y voit qu'une redirection. C'est la seule page qu'il atteigne,
// donc la seule où son en-tête se lise.
func TestLEnTeteProposeLaConnexionAuVisiteur(t *testing.T) {
	_, mux := serveurDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/connexion", nil).Body.String()

	if !strings.Contains(corps, `href="/connexion"`) {
		t.Errorf("en-tête sans lien de connexion :\n%s", corps)
	}
	if strings.Contains(corps, `action="/deconnexion"`) {
		t.Errorf("un visiteur se voit proposer de se déconnecter :\n%s", corps)
	}
}
