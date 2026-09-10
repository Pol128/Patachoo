package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seuilDeConnexion est le nombre de POST /connexion qu'une même adresse a le
// droit de jouer dans la fenêtre.
//
// Écrit en clair, et non relu depuis la migration : un test qui compare une
// constante à elle-même ne vérifie que lui-même. C'est ce chiffre-là que
// DOD.md §3 demande de garder sous test — « une valeur codée sans test finit
// augmentée temporairement ».
const seuilDeConnexion = 5

// Le plafond ne sert à rien s'il ne tombe pas : la sixième tentative d'une même
// adresse est refusée, la cinquième ne l'est pas.
//
// Et elle est refusée *avant* le gestionnaire, donc avant ValidatePassword :
// c'est tout l'intérêt du plafond, puisque chaque appel paie un bcrypt. La
// preuve tient dans le corps rendu — le message d'échec de connexion n'y est
// pas, or c'est le gestionnaire, et lui seul, qui le pose.
func TestLaSixiemeTentativeDeConnexionEstRefusee(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	const ip = "203.0.113.10"

	for tentative := 1; tentative <= seuilDeConnexion; tentative++ {
		rec := seConnecteDepuis(t, mux, ip, courrielDeTest, "pas-le-bon-mot-de-passe")
		if rec.Code != http.StatusOK {
			t.Fatalf("tentative %d : statut %d, attendu %d — le plafond tombe avant le seuil",
				tentative, rec.Code, http.StatusOK)
		}
		exigeContient(t, rec.Body.String(), messageEchecConnexion)
	}

	rec := seConnecteDepuis(t, mux, ip, courrielDeTest, "pas-le-bon-mot-de-passe")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("tentative %d : statut %d, attendu %d — la route de connexion n'a pas de plafond",
			seuilDeConnexion+1, rec.Code, http.StatusTooManyRequests)
	}
	exigeSansAucun(t, rec.Body.String(), messageEchecConnexion)
}

// Le dépassement de PocketBase sort en JSON (« Too Many Requests. »), écrit par
// router.ErrorHandler. Or /connexion est un formulaire HTML ordinaire : celui
// qui s'y trompe six fois de mot de passe verrait du JSON brut à la place de sa
// page.
func TestLeDepassementRendLaPageDeConnexionEnHTML(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	rec := epuiseLePlafond(t, mux, "203.0.113.11", courrielDeTest)

	if typeDeContenu := rec.Header().Get("Content-Type"); !strings.Contains(typeDeContenu, "text/html") {
		t.Errorf("Content-Type %q, attendu du text/html : le dépassement est rendu en JSON", typeDeContenu)
	}
	exigeContient(t, rec.Body.String(),
		`<form method="post" action="/connexion">`,
		`name="courriel"`,
		`name="mot-de-passe"`,
		`role="alert"`,
	)
}

// La page de connexion ne dit pas quels courriels existent, et le plafond ne
// doit pas rouvrir par un autre canal ce que le message d'échec ferme : poussés
// tous deux au-delà du seuil, un courriel connu et un inconnu rendent le même
// code et le même corps.
func TestLeMessageDeDepassementNeDitPasQuelsComptesExistent(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	connu := epuiseLePlafond(t, mux, "203.0.113.12", courrielDeTest)
	inconnu := epuiseLePlafond(t, mux, "203.0.113.13", "personne@exemple.fr")

	if connu.Code != inconnu.Code {
		t.Errorf("statut %d sur courriel connu contre %d sur courriel inconnu : le dépassement dit quels comptes existent",
			connu.Code, inconnu.Code)
	}
	if connu.Body.String() != inconnu.Body.String() {
		t.Errorf("le corps du dépassement diffère entre un courriel connu et un inconnu :\n--- connu ---\n%s\n--- inconnu ---\n%s",
			connu.Body.String(), inconnu.Body.String())
	}
}

// Un plafond compté par IP enfermerait tout le monde s'il était global. Une
// seconde adresse doit donc se connecter encore, la première épuisée.
func TestLePlafondNEnfermePasLesAutresClients(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	epuiseLePlafond(t, mux, "203.0.113.14", courrielDeTest)

	rec := seConnecteDepuis(t, mux, "203.0.113.15", courrielDeTest, motDePasseDeTest)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d pour une seconde adresse, attendu %d : le plafond d'une IP ferme la porte aux autres",
			rec.Code, http.StatusSeeOther)
	}
	if cookie := cookieEventuelDe(rec); cookie == nil {
		t.Error("aucun cookie de session pour une seconde adresse : sa connexion n'a pas abouti")
	}
}

// L'audience de la règle est « tous », et non « invités » : une session déjà
// ouverte compte comme les autres. Sans cela, un jeton valable — celui d'un
// compte quelconque, obtenu une fois — suffirait à marteler la route.
func TestLePlafondCompteAussiUneSessionOuverte(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	cookie := cookieDe(t, seConnecteDepuis(t, mux, "203.0.113.16", courrielDeTest, motDePasseDeTest))

	const ip = "203.0.113.17"
	for tentative := 1; tentative <= seuilDeConnexion; tentative++ {
		rec := tenteAvecCookie(t, mux, ip, cookie)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("tentative %d refusée alors que le seuil est de %d", tentative, seuilDeConnexion)
		}
	}

	rec := tenteAvecCookie(t, mux, ip, cookie)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("statut %d pour la tentative %d d'une session ouverte, attendu %d : le plafond ne vaut que pour les visiteurs",
			rec.Code, seuilDeConnexion+1, http.StatusTooManyRequests)
	}
}

// Le rattrapage HTML est posé sur la seule route de connexion. L'API REST, elle,
// parle JSON à ses clients : lui rendre une page de connexion serait remplacer
// une erreur lisible par du HTML qu'aucun client ne sait lire.
//
// Le dépassement s'obtient ici sur la règle `*:auth` livrée par PocketBase
// (2 requêtes par 3 s), que l'activation des réglages allume du même coup —
// c'est la conséquence assumée du choix de passer par eux.
func TestUnDepassementSurLAPIResteEnJSON(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	const ip = "203.0.113.18"

	var rec *httptest.ResponseRecorder
	for tentative := 1; tentative <= 3; tentative++ {
		rec = authentifieParLAPI(t, mux, ip)
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("statut %d à la troisième authentification par l'API, attendu %d : la règle *:auth ne s'applique pas",
			rec.Code, http.StatusTooManyRequests)
	}
	if typeDeContenu := rec.Header().Get("Content-Type"); !strings.Contains(typeDeContenu, "application/json") {
		t.Errorf("Content-Type %q sur l'API, attendu de l'application/json", typeDeContenu)
	}
	exigeSansAucun(t, rec.Body.String(), "<form")
}

// epuiseLePlafond joue une tentative de trop depuis la même adresse et rend la
// réponse au dépassement.
func epuiseLePlafond(t *testing.T, mux http.Handler, ip, courriel string) *httptest.ResponseRecorder {
	t.Helper()

	var rec *httptest.ResponseRecorder
	for tentative := 1; tentative <= seuilDeConnexion+1; tentative++ {
		rec = seConnecteDepuis(t, mux, ip, courriel, "pas-le-bon-mot-de-passe")
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("statut %d après %d tentatives depuis %s, attendu %d",
			rec.Code, seuilDeConnexion+1, ip, http.StatusTooManyRequests)
	}
	return rec
}

// tenteAvecCookie poste le formulaire en portant une session déjà ouverte.
func tenteAvecCookie(t *testing.T, mux http.Handler, ip string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/connexion",
		strings.NewReader("courriel="+courrielDeTest+"&mot-de-passe=pas-le-bon-mot-de-passe"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = net.JoinHostPort(ip, "1234")
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// authentifieParLAPI joue la route d'authentification de PocketBase, celle que
// nos pages n'empruntent pas.
func authentifieParLAPI(t *testing.T, mux http.Handler, ip string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/api/collections/users/auth-with-password",
		strings.NewReader(`{"identity":"`+courrielDeTest+`","password":"pas-le-bon-mot-de-passe"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = net.JoinHostPort(ip, "1234")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
