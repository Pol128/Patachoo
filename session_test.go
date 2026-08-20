package main

import (
	"context"
	"encoding/json"
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

	app := baseNeuveAvec(t, analyseurDeTest(t))

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
	routeur.GET("/sonde", func(e *core.RequestEvent) error {
		if e.Auth == nil {
			return e.String(http.StatusOK, "visiteur")
		}
		return e.String(http.StatusOK, e.Auth.Id)
	})
}

// sondeProtegee exige une session par le middleware de PocketBase, et non par
// une vérification à nous : c'est lui que les pages à venir emploieront.
func sondeProtegee(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/sonde-protegee", func(e *core.RequestEvent) error {
		return e.String(http.StatusOK, "ok")
	}).Bind(apis.RequireAuth())
}

// compteDeTest crée le compte dont les tests se servent pour se connecter.
func compteDeTest(t *testing.T, app core.App) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("collection users : %v", err)
	}

	compte := core.NewRecord(collection)
	compte.SetEmail(courrielDeTest)
	compte.SetPassword(motDePasseDeTest)
	compte.Set("name", nomDeTest)
	if err := app.Save(compte); err != nil {
		t.Fatalf("création du compte : %v", err)
	}
	return compte
}

// seConnecte poste le formulaire de connexion.
func seConnecte(t *testing.T, mux http.Handler, courriel, motDePasse string) *httptest.ResponseRecorder {
	t.Helper()

	champs := url.Values{"courriel": {courriel}, "mot-de-passe": {motDePasse}}
	req := httptest.NewRequest(http.MethodPost, "/connexion", strings.NewReader(champs.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
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

// cookieEventuelDe rend le cookie de session, ou nil s'il n'y en a pas.
func cookieEventuelDe(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range (&http.Response{Header: rec.Header()}).Cookies() {
		if cookie.Name == nomCookieSession {
			return cookie
		}
	}
	return nil
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

// journaux vide le tampon du journal et rend tout ce qu'il a écrit.
func journaux(t *testing.T, app core.App) string {
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
	compte := compteDeTest(t, app)

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
	compte := compteDeTest(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	rec := avecCookie(mux, http.MethodGet, "/sonde", cookie)

	if rec.Body.String() != compte.Id {
		t.Errorf("la sonde a reconnu %q, attendu %q", rec.Body.String(), compte.Id)
	}
}

func TestUnCookieInexploitableLaisseLaRequeteEnVisiteur(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compte := compteDeTest(t, app)

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
	compteDeTest(t, app)

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
	compteDeTest(t, app)

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

func TestLeMotDePasseNapparaitNiDansLaPageNiDansLesJournaux(t *testing.T) {
	const saisi = "sirop-de-liege-mal-tape"

	app, mux := serveurDeTest(t)
	compteDeTest(t, app)

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
	compteDeTest(t, app)

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

// --- Le renouvellement ----------------------------------------------------

func TestUnJetonFraisNestPasRenouvele(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	compteDeTest(t, app)

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
	compte := compteDeTest(t, app)

	// La durée réduite que demande le critère : un jeton émis pour trente
	// secondes est, par construction, très en deçà de la mi-vie de la
	// collection — cinq jours — sans qu'aucun test ait à attendre.
	court, err := compte.NewStaticAuthToken(30 * time.Second)
	if err != nil {
		t.Fatalf("émission du jeton court : %v", err)
	}

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

// --- Les règles d'accès ---------------------------------------------------

func TestUneRouteProtegeeExigeLaSession(t *testing.T) {
	app, mux := serveurDeTest(t, sondeProtegee)
	compteDeTest(t, app)

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
	compteDeTest(t, app)

	cookie := cookieDe(t, seConnecte(t, mux, courrielDeTest, motDePasseDeTest))
	corps := avecCookie(mux, http.MethodGet, "/", cookie).Body.String()

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

func TestLEnTeteProposeLaConnexionAuVisiteur(t *testing.T) {
	_, mux := serveurDeTest(t)

	corps := avecCookie(mux, http.MethodGet, "/", nil).Body.String()

	if !strings.Contains(corps, `href="/connexion"`) {
		t.Errorf("en-tête sans lien de connexion :\n%s", corps)
	}
	if strings.Contains(corps, `action="/deconnexion"`) {
		t.Errorf("un visiteur se voit proposer de se déconnecter :\n%s", corps)
	}
}
