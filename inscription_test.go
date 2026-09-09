package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

const courrielDInscription = "nouvelle@exemple.fr"

// --- Le réglage -----------------------------------------------------------

// leReglage rend l'unique enregistrement de settings, celui que la migration
// pose. Deux en base serait une anomalie, et un test qui en tirerait un au
// hasard ne dirait plus lequel il vient de basculer.
func leReglage(t *testing.T, app core.App) *core.Record {
	t.Helper()

	reglages, err := app.FindAllRecords("settings")
	if err != nil {
		t.Fatalf("lecture de settings : %v", err)
	}
	if len(reglages) != 1 {
		t.Fatalf("%d enregistrements dans settings, attendu 1", len(reglages))
	}
	return reglages[0]
}

// ouvreLInscription bascule le réglage en base, exactement comme le ferait un
// clic dans l'administration sur /_/ — sans redémarrer quoi que ce soit.
func ouvreLInscription(t *testing.T, app core.App) {
	t.Helper()

	reglage := leReglage(t, app)
	reglage.Set("open_registration", true)
	if err := app.Save(reglage); err != nil {
		t.Fatalf("ouverture de l'inscription : %v", err)
	}
}

// supprimeLeReglage rejoue l'accident : une base à moitié migrée, ou un
// enregistrement effacé à la main.
func supprimeLeReglage(t *testing.T, app core.App) {
	t.Helper()

	if err := app.Delete(leReglage(t, app)); err != nil {
		t.Fatalf("suppression du réglage : %v", err)
	}
}

// --- Les requêtes ---------------------------------------------------------

// champsDInscription rend un formulaire valide, que chaque test abîme à sa
// façon.
func champsDInscription() url.Values {
	return url.Values{
		"email":           {courrielDInscription},
		"password":        {motDePasseDeTest},
		"passwordConfirm": {motDePasseDeTest},
		"name":            {nomDeTest},
	}
}

func sInscrit(t *testing.T, mux http.Handler, champs url.Values) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/inscription", strings.NewReader(champs.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func nombreDeComptes(t *testing.T, app core.App) int {
	t.Helper()

	comptes, err := app.FindAllRecords("users")
	if err != nil {
		t.Fatalf("lecture des comptes : %v", err)
	}
	return len(comptes)
}

// --- Réglage fermé --------------------------------------------------------

// Fermée, la page n'existe pas : un 404, et non un refus poli qui dirait
// qu'il y a quelque chose derrière.
func TestLaPageDInscriptionEstIntrouvableQuandLeReglageEstFerme(t *testing.T) {
	_, mux := serveurDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/inscription", nil)

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
}

func TestLInscriptionEstRefuseeQuandLeReglageEstFerme(t *testing.T) {
	app, mux := serveurDeTest(t)
	avant := nombreDeComptes(t, app)

	rec := sInscrit(t, mux, champsDInscription())

	if rec.Code != http.StatusNotFound {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if apres := nombreDeComptes(t, app); apres != avant {
		t.Errorf("%d comptes après le refus, attendu %d", apres, avant)
	}
	if cookie := cookieEventuelDe(rec); cookie != nil {
		t.Error("un cookie de session part d'une inscription refusée")
	}
}

// Le défaut d'un réglage de sécurité se choisit du côté qui refuse : une base
// à moitié migrée, ou un enregistrement effacé à la main, ne doit pas ouvrir
// l'inscription par accident.
func TestSansEnregistrementDeReglageLInscriptionEstRefusee(t *testing.T) {
	app, mux := serveurDeTest(t)
	ouvreLInscription(t, app)
	supprimeLeReglage(t, app)
	avant := nombreDeComptes(t, app)

	if rec := avecCookie(mux, http.MethodGet, "/inscription", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if rec := sInscrit(t, mux, champsDInscription()); rec.Code != http.StatusNotFound {
		t.Errorf("POST : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}
	if apres := nombreDeComptes(t, app); apres != avant {
		t.Errorf("%d comptes après le refus, attendu %d", apres, avant)
	}
}

// --- Réglage ouvert -------------------------------------------------------

func TestLaPageDInscriptionPorteLeFormulaireQuandLeReglageEstOuvert(t *testing.T) {
	app, mux := serveurDeTest(t)
	ouvreLInscription(t, app)

	rec := avecCookie(mux, http.MethodGet, "/inscription", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	corps := rec.Body.String()
	for _, attendu := range []string{
		`method="post"`, `action="/inscription"`,
		`name="email"`, `name="password"`, `name="passwordConfirm"`, `name="name"`,
	} {
		if !strings.Contains(corps, attendu) {
			t.Errorf("formulaire sans %q :\n%s", attendu, corps)
		}
	}
}

// L'inscription connecte : elle ne renvoie pas vers un second formulaire.
func TestUneInscriptionCreeLeCompteEtOuvreLaSession(t *testing.T) {
	app, mux := serveurDeTest(t, sonde)
	ouvreLInscription(t, app)

	rec := sInscrit(t, mux, champsDInscription())

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if lieu := rec.Header().Get("Location"); lieu != "/" {
		t.Errorf("redirigé vers %q, attendu %q", lieu, "/")
	}

	compte, err := app.FindAuthRecordByEmail("users", courrielDInscription)
	if err != nil {
		t.Fatalf("compte %q introuvable après l'inscription : %v", courrielDInscription, err)
	}
	if nom := compte.GetString("name"); nom != nomDeTest {
		t.Errorf("name = %q, attendu %q", nom, nomDeTest)
	}

	// Les mêmes attributs que la connexion : un cookie qui différerait d'un
	// seul d'entre eux serait une seconde session, aux règles à part.
	cookie := cookieDe(t, rec)
	attributsDeSession(t, cookie)
	if duree := int(compte.Collection().AuthToken.Duration); cookie.MaxAge != duree {
		t.Errorf("Max-Age %d, attendu %d — la durée de vie du jeton", cookie.MaxAge, duree)
	}

	if suivante := avecCookie(mux, http.MethodGet, "/sonde", cookie); suivante.Body.String() != compte.Id {
		t.Errorf("la sonde a reconnu %q, attendu %q : l'inscrit n'est pas connecté",
			suivante.Body.String(), compte.Id)
	}
}

// Le réglage se lit à chaque requête, jamais au démarrage : la même instance
// change de comportement dès que l'enregistrement change.
func TestLeReglageBasculeSansRedemarrage(t *testing.T) {
	app, mux := serveurDeTest(t)

	if rec := avecCookie(mux, http.MethodGet, "/inscription", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("avant la bascule : statut %d, attendu %d", rec.Code, http.StatusNotFound)
	}

	ouvreLInscription(t, app)

	if rec := avecCookie(mux, http.MethodGet, "/inscription", nil); rec.Code != http.StatusOK {
		t.Errorf("après la bascule : statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if rec := sInscrit(t, mux, champsDInscription()); rec.Code != http.StatusSeeOther {
		t.Errorf("après la bascule : POST rend %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
}

// --- La liste blanche -----------------------------------------------------

// Recopier le formulaire en vrac dans l'enregistrement laisserait un inscrit
// se poser verified à vrai, ou écrire tout champ ajouté plus tard à users sans
// que personne y repense.
func TestUnChampHorsListeBlancheNEstPasRecopie(t *testing.T) {
	app, mux := serveurDeTest(t)
	ouvreLInscription(t, app)

	champs := champsDInscription()
	champs.Set("verified", "true")
	champs.Set("emailVisibility", "true")

	if rec := sInscrit(t, mux, champs); rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	compte, err := app.FindAuthRecordByEmail("users", courrielDInscription)
	if err != nil {
		t.Fatalf("compte introuvable : %v", err)
	}
	if compte.Verified() {
		t.Error("l'inscrit s'est posé verified à vrai depuis le formulaire")
	}
	if compte.GetBool("emailVisibility") {
		t.Error("l'inscrit a écrit emailVisibility depuis le formulaire")
	}
}

// --- Les refus ------------------------------------------------------------

// Le refus, et rien de plus : dire que le courriel est déjà pris ferait de la
// page d'inscription un annuaire des comptes existants.
func TestUneInscriptionSurUnCourrielDejaPrisEstRefusee(t *testing.T) {
	app, mux := serveurDeTest(t)
	ouvreLInscription(t, app)
	creeCompte(t, app, courrielDInscription, "Déjà là")
	avant := nombreDeComptes(t, app)

	rec := sInscrit(t, mux, champsDInscription())

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	corps := rec.Body.String()
	if !strings.Contains(corps, messageEchecInscription) {
		t.Errorf("réponse sans le message de refus :\n%s", corps)
	}
	if strings.Contains(corps, courrielDInscription) {
		t.Errorf("la réponse renvoie le courriel saisi :\n%s", corps)
	}
	if apres := nombreDeComptes(t, app); apres != avant {
		t.Errorf("%d comptes après le refus, attendu %d", apres, avant)
	}
	if cookie := cookieEventuelDe(rec); cookie != nil {
		t.Error("un cookie de session part d'une inscription refusée")
	}
}

// La confirmation ne sert à rien si personne ne la lit.
func TestUneInscriptionMalConfirmeeNeCreeAucunCompte(t *testing.T) {
	app, mux := serveurDeTest(t)
	ouvreLInscription(t, app)
	avant := nombreDeComptes(t, app)

	champs := champsDInscription()
	champs.Set("passwordConfirm", "pas-le-meme")

	rec := sInscrit(t, mux, champs)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), messageConfirmationDifferente) {
		t.Errorf("réponse sans le message de confirmation :\n%s", rec.Body.String())
	}
	if apres := nombreDeComptes(t, app); apres != avant {
		t.Errorf("%d comptes après le refus, attendu %d", apres, avant)
	}
}

// --- La seule porte -------------------------------------------------------

// Le réglage ne vaudrait rien si l'API REST gardait la sienne : PocketBase
// livre users avec une createRule vide, donc publique.
func TestLaCreationDeCompteParLAPIRestEstRefuseeQuelQueSoitLeReglage(t *testing.T) {
	for _, ouvert := range []bool{false, true} {
		nom := "réglage fermé"
		if ouvert {
			nom = "réglage ouvert"
		}
		t.Run(nom, func(t *testing.T) {
			app, mux := serveurDeTest(t)
			if ouvert {
				ouvreLInscription(t, app)
			}
			avant := nombreDeComptes(t, app)

			corps := `{"email":"api@exemple.fr","password":"` + motDePasseDeTest +
				`","passwordConfirm":"` + motDePasseDeTest + `"}`
			req := httptest.NewRequest(http.MethodPost, "/api/collections/users/records", strings.NewReader(corps))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if apres := nombreDeComptes(t, app); apres != avant {
				t.Errorf("%d comptes après le refus, attendu %d", apres, avant)
			}
		})
	}
}

// --- Le lien depuis la connexion ------------------------------------------

// Une page fermée dont le lien resterait affiché enverrait tout le monde sur
// un 404 ; un lien absent quand la page est ouverte la rend introuvable.
func TestLaPageDeConnexionPorteLeLienDInscriptionSelonLeReglage(t *testing.T) {
	t.Run("réglage ouvert", func(t *testing.T) {
		app, mux := serveurDeTest(t)
		ouvreLInscription(t, app)

		corps := avecCookie(mux, http.MethodGet, "/connexion", nil).Body.String()

		if !strings.Contains(corps, `href="/inscription"`) {
			t.Errorf("page de connexion sans lien vers l'inscription :\n%s", corps)
		}
	})

	t.Run("réglage fermé", func(t *testing.T) {
		_, mux := serveurDeTest(t)

		corps := avecCookie(mux, http.MethodGet, "/connexion", nil).Body.String()

		if strings.Contains(corps, `href="/inscription"`) {
			t.Errorf("page de connexion avec un lien vers une page fermée :\n%s", corps)
		}
	})
}
