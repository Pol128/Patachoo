package main

import (
	"bytes"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/Pol128/Patachoo/recuperation"
)

// --- Les deux moitiés de la double soumission ------------------------------

// Le nom du cookie et celui du champ sont écrits en clair, et non repris des
// constantes du paquet : ce sont des noms de protocole, que le navigateur et le
// gabarit se partagent. Un test qui les lirait dans le code passerait encore
// après un renommage, lequel invaliderait pourtant tous les onglets ouverts.
const (
	nomDuCookieAttendu = "__Host-patachoo_antirejeu"
	nomDuChampAttendu  = "_antirejeu"
)

// jetonDeTest est la valeur que les tests posent des deux côtés de la paire
// quand ils n'exercent pas la pose elle-même : le cookie et le champ caché
// portent la même chose, ce qui est l'état nominal d'un formulaire rendu par le
// serveur.
const jetonDeTest = "jeton-anti-rejeu-de-test"

// cookieDuJetonDeTest rend le cookie qu'un navigateur joindrait à la requête.
func cookieDuJetonDeTest() *http.Cookie {
	return &http.Cookie{Name: nomDuCookieAttendu, Value: jetonDeTest}
}

// leJetonEstPose ajoute le champ caché à une saisie urlencodée.
//
// La saisie est modifiée sur place, comme le ferait le navigateur avec le champ
// que le gabarit porte : les tests partent tous d'un url.Values fabriqué pour
// eux, et aucun ne réutilise le sien après l'envoi.
func leJetonEstPose(champs url.Values) url.Values {
	if champs == nil {
		champs = url.Values{}
	}
	champs.Set(nomDuChampAttendu, jetonDeTest)
	return champs
}

// champHiddenDuJeton capte la valeur recopiée par le gabarit dans le
// formulaire. La forme est celle que le déroulé de la tâche fixe, attribut par
// attribut : un champ rendu autrement ne serait pas celui que le navigateur
// poste.
var champHiddenDuJeton = regexp.MustCompile(
	`<input type="hidden" name="` + nomDuChampAttendu + `" value="([^"]*)">`)

// --- Le montage des requêtes ----------------------------------------------

// postDeTest décrit l'une des douze routes POST du produit : son chemin, la
// saisie qu'un navigateur y enverrait, et l'encodage du formulaire qui la sert.
type postDeTest struct {
	nom       string
	cible     string
	champs    url.Values
	multipart bool
	// sansSession dit que la route se joue sans cookie de session : la
	// connexion et l'inscription sont les seules du lot à s'adresser à un
	// visiteur, et leur poster une session ouverte exercerait un autre chemin.
	sansSession bool
}

// joueLePost poste la saisie sur la route, avec le jeton donné dans le champ
// caché et celui donné dans le cookie.
//
// Les deux moitiés sont des paramètres distincts, et c'est tout l'objet du
// fichier : le champ vide dit « aucun jeton soumis », deux valeurs différentes
// disent « un jeton d'une autre session ». Une chaîne vide côté cookie n'en
// joint aucun.
func joueLePost(t *testing.T, mux http.Handler, p postDeTest, session *http.Cookie, jetonDuChamp, jetonDuCookie string) *httptest.ResponseRecorder {
	t.Helper()

	champs := url.Values{}
	for nom, valeurs := range p.champs {
		champs[nom] = append([]string(nil), valeurs...)
	}
	if jetonDuChamp != "" {
		champs.Set(nomDuChampAttendu, jetonDuChamp)
	}

	var req *http.Request
	if p.multipart {
		corps := &bytes.Buffer{}
		ecrivain := multipart.NewWriter(corps)
		for nom, valeurs := range champs {
			for _, valeur := range valeurs {
				if err := ecrivain.WriteField(nom, valeur); err != nil {
					t.Fatalf("champ %q : %v", nom, err)
				}
			}
		}
		if err := ecrivain.Close(); err != nil {
			t.Fatalf("clôture du corps multipart : %v", err)
		}
		req = httptest.NewRequest(http.MethodPost, p.cible, corps)
		req.Header.Set("Content-Type", ecrivain.FormDataContentType())
	} else {
		req = httptest.NewRequest(http.MethodPost, p.cible, strings.NewReader(champs.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	if session != nil && !p.sansSession {
		req.AddCookie(session)
	}
	if jetonDuCookie != "" {
		req.AddCookie(&http.Cookie{Name: nomDuCookieAttendu, Value: jetonDuCookie})
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// lesDouzePost rend les douze routes POST du produit, nommément, dans l'ordre
// où brancheLesRoutes les pose.
//
// Nommément, et non par une lecture du routeur : une route ajoutée demain
// n'apparaîtrait pas ici, et c'est précisément ce que le relevé doit faire
// remarquer à celui qui l'ajoute.
func lesDouzePost(recette, note, ligne *core.Record) []postDeTest {
	notes := "/recettes/" + recette.Id + "/commentaires"
	return []postDeTest{
		{nom: "connexion", cible: "/connexion", sansSession: true,
			champs: url.Values{"courriel": {courrielDeTest}, "mot-de-passe": {motDePasseDeTest}}},
		{nom: "déconnexion", cible: "/deconnexion"},
		{nom: "inscription", cible: "/inscription", sansSession: true,
			champs: url.Values{
				"email":           {courrielDInscription},
				"password":        {motDePasseDeTest},
				"passwordConfirm": {motDePasseDeTest},
				"name":            {nomDeTest},
			}},
		{nom: "création d'une recette", cible: "/recettes", champs: champsValides(), multipart: true},
		{nom: "édition d'une recette", cible: "/recettes/" + recette.Id, champs: champsValides(), multipart: true},
		{nom: "suppression d'une recette", cible: "/recettes/" + recette.Id + "/supprimer"},
		{nom: "import unitaire", cible: "/recettes/importer",
			champs: url.Values{"url": {"https://exemple.fr/recette"}}},
		{nom: "lancement d'un lot", cible: cheminDuLot,
			champs: url.Values{"urls": {"https://exemple.fr/recette"}}},
		{nom: "reprise d'une adresse en échec",
			cible: lienDeLaReprise(ligne.GetString("batch"), ligne.Id)},
		{nom: "ajout d'une note", cible: notes, champs: corpsDe("Trop cuit de dix minutes.")},
		{nom: "modification d'une note", cible: notes + "/" + note.Id, champs: corpsDe("Finalement, très bien.")},
		{nom: "suppression d'une note", cible: notes + "/" + note.Id + "/supprimer"},
	}
}

// atelierAntiRejeu monte le carnet, une recette signée du compte de la session,
// une note à lui et une fournée à lui dont une adresse a échoué : les douze
// routes ont ainsi toutes une cible réelle, et un refus qui viendrait d'un
// identifiant inconnu ne pourrait pas se confondre avec celui du jeton.
//
// Le réseau est piégé : aucune des douze ne doit sortir, et l'import est la
// seule qui le ferait si le gestionnaire s'exécutait.
func atelierAntiRejeu(t *testing.T) (core.App, http.Handler, *http.Cookie, *core.Record, *core.Record, *core.Record) {
	t.Helper()

	app, mux, cookie := atelierDeLot(t)
	ouvreLInscription(t, app)
	recette := laSienne(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Une note.")
	lot := clot(t, app, lotEnBase(t, app, leCompteDeLaSession(t, app),
		ligneVoulue{url: "https://exemple.fr/muette", statut: statutEchec, cause: recuperation.DelaiDepasse}))
	return app, mux, cookie, recette, note, laLigne(t, app, lot, "https://exemple.fr/muette")
}

// --- Le refus, route par route --------------------------------------------

// Un POST sans champ _antirejeu est refusé par un 403, quelle que soit la
// route : c'est la page tierce qui soumet toute seule un formulaire qu'elle a
// écrit, et qui n'a aucun moyen de connaître le jeton.
func TestLesDouzeRoutesPostRefusentUnePostSansJeton(t *testing.T) {
	app, mux, session, recette, note, ligne := atelierAntiRejeu(t)

	for _, p := range lesDouzePost(recette, note, ligne) {
		t.Run(p.nom, func(t *testing.T) {
			rec := joueLePost(t, mux, p, session, "", jetonDeTest)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			if typeDeContenu := rec.Header().Get("Content-Type"); !strings.Contains(typeDeContenu, "text/html") {
				t.Errorf("Content-Type %q, attendu du text/html : un formulaire ne lit pas le JSON", typeDeContenu)
			}
			exigeContient(t, rec.Body.String(), "Formulaire expiré")
		})
	}

	// Le gestionnaire ne s'est exécuté nulle part : ni recette de plus, ni note
	// touchée, ni recette supprimée.
	if !laRecetteEstEnBase(t, app, recette.Id) {
		t.Errorf("la recette a été supprimée par un POST sans jeton")
	}
	if notes := notesDe(t, app, recette); len(notes) != 1 || notes[0].GetString("body") != "Une note." {
		t.Errorf("les notes ont bougé sous un POST sans jeton : %v", notes)
	}
}

// Un POST dont le champ ne correspond pas au cookie est refusé de la même
// façon : c'est le jeton d'une autre session, ou celui qu'un voisin same-site
// aurait fourni sans pouvoir fournir le cookie que le préfixe __Host- lui
// interdit.
func TestLesDouzeRoutesPostRefusentUnJetonQuiNeCorrespondPas(t *testing.T) {
	_, mux, session, recette, note, ligne := atelierAntiRejeu(t)

	for _, p := range lesDouzePost(recette, note, ligne) {
		t.Run(p.nom, func(t *testing.T) {
			rec := joueLePost(t, mux, p, session, "le-jeton-d-une-autre-session", jetonDeTest)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
			exigeContient(t, rec.Body.String(), "Formulaire expiré")
		})
	}
}

// --- Les deux routes que SameSite=Lax ne couvrait pas ----------------------

// La fermeture du constat : une connexion refusée pour ce motif n'émet aucun
// Set-Cookie de session. C'est le dépôt du cookie de l'attaquant chez la
// victime que l'attaque demande, et c'est lui qui ne part plus.
func TestUneConnexionRefuseeNemetAucunCookieDeSession(t *testing.T) {
	app, mux := serveurDeTest(t)
	compteParDefaut(t, app)

	rec := joueLePost(t, mux, postDeTest{
		cible:  "/connexion",
		champs: url.Values{"courriel": {courrielDeTest}, "mot-de-passe": {motDePasseDeTest}},
	}, nil, "", jetonDeTest)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusForbidden)
	}
	if poses := cookiesDeSession(rec); len(poses) != 0 {
		t.Errorf("%d cookie(s) de session dans la réponse refusée : %v", len(poses), poses)
	}
}

// Le même geste sur l'inscription, qui offrait le parcours en une seule requête
// et sans compte préalable : aucun enregistrement n'est créé dans users.
func TestUneInscriptionRefuseeNeCreeAucunCompte(t *testing.T) {
	app, mux := serveurDeTest(t)
	ouvreLInscription(t, app)
	avant := nombreDeComptes(t, app)

	rec := joueLePost(t, mux, postDeTest{
		cible:  "/inscription",
		champs: champsDInscription(),
	}, nil, "", jetonDeTest)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusForbidden)
	}
	if apres := nombreDeComptes(t, app); apres != avant {
		t.Errorf("%d comptes après le refus, attendu %d", apres, avant)
	}
	if poses := cookiesDeSession(rec); len(poses) != 0 {
		t.Errorf("%d cookie(s) de session dans la réponse refusée : %v", len(poses), poses)
	}
}

// --- Le cookie -------------------------------------------------------------

// cookieAntiRejeuDe rend le cookie anti-rejeu posé par la réponse, ou nil.
func cookieAntiRejeuDe(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range (&http.Response{Header: rec.Header()}).Cookies() {
		if cookie.Name == nomDuCookieAttendu {
			return cookie
		}
	}
	return nil
}

// Le cookie neuf porte le préfixe __Host- et les attributs qui le rendent
// valide : sans Secure, sans Path=/ ou avec un Domain, le navigateur le
// refuserait tout entier, et la paire ne tiendrait plus.
func TestLeCookieAntiRejeuPorteSesAttributs(t *testing.T) {
	_, mux := serveurDeTest(t)

	rec := avecCookie(mux, http.MethodGet, "/connexion", nil)
	cookie := cookieAntiRejeuDe(rec)
	if cookie == nil {
		t.Fatalf("aucun cookie %q dans la réponse : %q", nomDuCookieAttendu, rec.Header().Values("Set-Cookie"))
	}

	if cookie.Value == "" {
		t.Error("cookie anti-rejeu vide")
	}
	if !cookie.Secure {
		t.Error("cookie sans Secure : le préfixe __Host- l'exige")
	}
	if !cookie.HttpOnly {
		t.Error("cookie sans HttpOnly : c'est le serveur qui recopie la valeur, aucun JavaScript n'a à la lire")
	}
	if cookie.Path != "/" {
		t.Errorf("Path %q, attendu %q : le préfixe __Host- l'exige", cookie.Path, "/")
	}
	if cookie.Domain != "" {
		t.Errorf("Domain %q, attendu vide : le préfixe __Host- l'interdit", cookie.Domain)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite %v, attendu Lax", cookie.SameSite)
	}
	if cookie.MaxAge <= 0 {
		t.Errorf("Max-Age %d, attendu la durée du jeton d'authentification", cookie.MaxAge)
	}
}

// Deux visiteurs n'obtiennent pas le même jeton : une valeur devinable
// rendrait la paire décorative.
func TestDeuxVisiteursObtiennentDesJetonsDifferents(t *testing.T) {
	_, mux := serveurDeTest(t)

	premier := cookieAntiRejeuDe(avecCookie(mux, http.MethodGet, "/connexion", nil))
	second := cookieAntiRejeuDe(avecCookie(mux, http.MethodGet, "/connexion", nil))
	if premier == nil || second == nil {
		t.Fatal("un des deux visiteurs n'a reçu aucun cookie anti-rejeu")
	}
	if premier.Value == second.Value {
		t.Errorf("les deux visiteurs portent le même jeton %q", premier.Value)
	}
}

// Un navigateur qui porte déjà un cookie valide le garde : le reposer à chaque
// requête périmerait les champs cachés des onglets déjà ouverts.
func TestUnCookieDejaPoseNestPasRemplace(t *testing.T) {
	_, mux := serveurDeTest(t)

	req := httptest.NewRequest(http.MethodGet, "/connexion", nil)
	req.AddCookie(cookieDuJetonDeTest())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if cookie := cookieAntiRejeuDe(rec); cookie != nil {
		t.Errorf("le cookie a été reposé (%q) alors que la requête en portait un valide", cookie.Value)
	}
	if jeton := jetonDuFormulaire(t, rec.Body.String()); jeton != jetonDeTest {
		t.Errorf("le formulaire porte %q, attendu %q : le gabarit ne recopie pas le cookie reçu", jeton, jetonDeTest)
	}
}

// --- La portée de la pose -------------------------------------------------

// La pose s'arrête aux chemins qui ne servent aucun gabarit : /statique/,
// /api/ et /_/. Le test est dans le sens du refus, et il tient à deux titres.
//
// Le premier est une fuite. Ce sont exactement les chemins que
// cheminsSansCacheControl laisse délibérément mettre en cache. Une feuille de
// style qui emporterait le Set-Cookie du jeton passerait par un cache partagé
// réglé pour ignorer Set-Cookie sur les assets — proxy_ignore_headers
// Set-Cookie, recette nginx courante —, qui rejouerait la même valeur à tous
// les visiteurs. L'attaquant n'aurait plus qu'à demander /statique/patachoo.css,
// lire le jeton dans l'en-tête et le recopier dans son champ caché : la
// comparaison passerait, et le constat que cette tâche ferme serait rouvert.
//
// Le second est une nuisance. Une sous-ressource cross-site — <img
// src="https://…/statique/patachoo.css"> — n'emporte pas le cookie sous
// SameSite=Lax ; là où le navigateur accepte encore les cookies tiers, la pose
// en tirerait un neuf et écraserait celui du navigateur, faisant tomber en 403
// les formulaires que la victime avait ouverts.
func TestLaPoseSArreteAuxCheminsQuiNeServentAucunGabarit(t *testing.T) {
	_, mux := serveurDeTest(t)

	for _, cas := range []struct {
		nom   string
		cible string
	}{
		{"un asset", "/statique/patachoo.css"},
		{"une collection de l'API", "/api/collections/recipes/records"},
		{"le panneau d'administration", "/_/"},
	} {
		t.Run(cas.nom, func(t *testing.T) {
			rec := avecCookie(mux, http.MethodGet, cas.cible, nil)
			if rec.Code == http.StatusNotFound {
				t.Fatalf("%q ne mène nulle part : le test ne prouverait rien", cas.cible)
			}
			if cookie := cookieAntiRejeuDe(rec); cookie != nil {
				t.Errorf("%q pose %s=%q — aucune de ces réponses n'a de champ caché à garnir, et toutes sont mises en cache",
					cas.cible, nomDuCookieAttendu, cookie.Value)
			}
		})
	}
}

// --- Le jeton dans les gabarits -------------------------------------------

// jetonDuFormulaire extrait la valeur du champ caché d'une page, ou fait
// échouer le test.
func jetonDuFormulaire(t *testing.T, corps string) string {
	t.Helper()

	trouve := champHiddenDuJeton.FindStringSubmatch(corps)
	if trouve == nil {
		t.Fatalf("aucun champ %q dans la page :\n%s", nomDuChampAttendu, corps)
	}
	return trouve[1]
}

// Aucun formulaire n'est oublié : dans cmd/patachoo/vues/, le nombre de
// formulaires en POST et le nombre de champs cachés sont égaux.
//
// Le décompte se fait sur le système de fichiers embarqué, celui qui part dans
// le binaire : un gabarit oublié de l'embed ne serait pas servi.
func TestChaqueFormulaireEnPostPorteLeChampAntiRejeu(t *testing.T) {
	const attendus = 10

	var formulaires, champs int
	err := fs.WalkDir(vues, ".", func(chemin string, entree fs.DirEntry, err error) error {
		if err != nil || entree.IsDir() {
			return err
		}
		contenu, err := fs.ReadFile(vues, chemin)
		if err != nil {
			return err
		}
		formulaires += strings.Count(string(contenu), `method="post"`)
		champs += strings.Count(string(contenu), `name="`+nomDuChampAttendu+`"`)
		return nil
	})
	if err != nil {
		t.Fatalf("parcours des gabarits : %v", err)
	}

	if formulaires != attendus {
		t.Errorf("%d formulaires en POST dans les gabarits, %d attendus : le relevé de la tâche a bougé",
			formulaires, attendus)
	}
	if champs != formulaires {
		t.Errorf("%d champs %q pour %d formulaires en POST : un formulaire est oublié",
			champs, nomDuChampAttendu, formulaires)
	}
}

// Le jeton recopié dans la page est celui du cookie de la réponse : c'est la
// paire, et une page qui rendrait autre chose refuserait sa propre soumission.
//
// Les pages retenues sont celles qui portent les formulaires, y compris la mise
// en page elle-même — la déconnexion vit dans son en-tête, sous un {{with}}, et
// c'est le piège que le déroulé signale.
func TestChaquePageRendLeJetonDeSonCookie(t *testing.T) {
	_, mux, session, recette, _, ligne := atelierAntiRejeu(t)

	pages := []struct {
		nom         string
		cible       string
		avecSession bool
	}{
		{nom: "connexion", cible: "/connexion"},
		{nom: "inscription", cible: "/inscription"},
		{nom: "mise en page et déconnexion", cible: "/recettes", avecSession: true},
		{nom: "nouvelle recette", cible: "/recettes/nouvelle", avecSession: true},
		{nom: "édition d'une recette", cible: "/recettes/" + recette.Id + "/modifier", avecSession: true},
		{nom: "suppression d'une recette", cible: "/recettes/" + recette.Id + "/supprimer", avecSession: true},
		{nom: "import unitaire", cible: "/recettes/importer", avecSession: true},
		{nom: "import en lot", cible: cheminDuLot, avecSession: true},
		// Le rapport d'une fournée porte un formulaire par adresse en échec,
		// et c'est la seule page dont les formulaires se comptent par ligne.
		{nom: "rapport d'une fournée", cible: lienDuSuivi(ligne.GetString("batch")), avecSession: true},
		{nom: "notes de la fiche", cible: "/recettes/" + recette.Id, avecSession: true},
	}

	for _, p := range pages {
		t.Run(p.nom, func(t *testing.T) {
			var cookie *http.Cookie
			if p.avecSession {
				cookie = session
			}
			rec := avecCookie(mux, http.MethodGet, p.cible, cookie)
			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
			}

			pose := cookieAntiRejeuDe(rec)
			if pose == nil {
				t.Fatalf("aucun cookie anti-rejeu posé par %q", p.cible)
			}
			if jeton := jetonDuFormulaire(t, rec.Body.String()); jeton != pose.Value {
				t.Errorf("le formulaire porte %q, le cookie %q : la paire ne tient pas", jeton, pose.Value)
			}
		})
	}
}

// --- Le parcours nominal, de bout en bout ---------------------------------

// La page rendue par le serveur se soumet : on lit le cookie et le champ caché
// de la page, on les renvoie, et la recette est créée — image comprise, donc en
// multipart, l'encodage où le middleware lit le champ par valeursSoumises.
func TestLeFormulaireRenduParLeServeurSeSoumet(t *testing.T) {
	app, mux, session := carnetDeTest(t)

	page := avecCookie(mux, http.MethodGet, "/recettes/nouvelle", session)
	if page.Code != http.StatusOK {
		t.Fatalf("statut %d sur le formulaire, attendu %d", page.Code, http.StatusOK)
	}
	cookie := cookieAntiRejeuDe(page)
	if cookie == nil {
		t.Fatal("aucun cookie anti-rejeu sur la page du formulaire")
	}

	champs := champsValides()
	champs.Set(nomDuChampAttendu, jetonDuFormulaire(t, page.Body.String()))

	avant := len(recettes(t, app))
	rec := posteAvecJeton(t, mux, "/recettes", session, cookie, champs,
		fichierPoste{nom: "tarte.png", contenu: pngDeTest(t)})

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if apres := len(recettes(t, app)); apres != avant+1 {
		t.Errorf("%d recettes après la création, attendu %d", apres, avant+1)
	}
}

// posteAvecJeton poste un formulaire multipart en joignant les deux cookies.
func posteAvecJeton(t *testing.T, mux http.Handler, cible string, session, jeton *http.Cookie, champs url.Values, fichiers ...fichierPoste) *httptest.ResponseRecorder {
	t.Helper()

	corps := &bytes.Buffer{}
	ecrivain := multipart.NewWriter(corps)
	for nom, valeurs := range champs {
		for _, valeur := range valeurs {
			if err := ecrivain.WriteField(nom, valeur); err != nil {
				t.Fatalf("champ %q : %v", nom, err)
			}
		}
	}
	for _, fichier := range fichiers {
		partie, err := ecrivain.CreateFormFile("image", fichier.nom)
		if err != nil {
			t.Fatalf("fichier %q : %v", fichier.nom, err)
		}
		if _, err := partie.Write(fichier.contenu); err != nil {
			t.Fatalf("écriture de %q : %v", fichier.nom, err)
		}
	}
	if err := ecrivain.Close(); err != nil {
		t.Fatalf("clôture du corps multipart : %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, cible, corps)
	req.Header.Set("Content-Type", ecrivain.FormDataContentType())
	if session != nil {
		req.AddCookie(session)
	}
	if jeton != nil {
		req.AddCookie(jeton)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// --- Ce que le jeton n'exige pas ------------------------------------------

// Rien n'est exigé sous /api/ : un client qui porte son jeton dans
// Authorization crée toujours une recette, sans champ _antirejeu. Le contrôle
// se pose route par route, et pas sur le routeur, précisément pour cela.
func TestLAPIRestNexigeAucunJeton(t *testing.T) {
	app, mux := serveurDeTest(t)
	compte := compteParDefaut(t, app)

	jeton, err := compte.NewAuthToken()
	if err != nil {
		t.Fatalf("émission du jeton : %v", err)
	}

	avant := len(recettes(t, app))
	req := httptest.NewRequest(http.MethodPost, "/api/collections/recipes/records",
		strings.NewReader(`{"title":"Tarte aux pommes"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", jeton)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d — corps :\n%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if apres := len(recettes(t, app)); apres != avant+1 {
		t.Errorf("%d recettes après la création par l'API, attendu %d", apres, avant+1)
	}
}

// --- Le couplage des deux middlewares -------------------------------------

// Le contrôle refuse tout quand la pose n'a pas tourné, plutôt que de laisser
// passer un POST au champ vide.
//
// Ce n'est pas un état que le produit atteint : brancheLesRoutes pose les deux
// middlewares ensemble. C'est celui qu'un branchement futur pourrait
// atteindre, et il s'ouvrirait alors sans bruit sur les douze routes — la
// comparaison de deux chaînes vides est vraie. La route témoin reproduit
// exactement ce cas sur le montage réel, en écartant d'elle le seul middleware
// de pose.
func TestLeControleRefuseQuandLaPoseNaPasTourne(t *testing.T) {
	_, mux := serveurDeTest(t, func(routeur *router.Router[*core.RequestEvent]) {
		routeur.POST("/sonde-antirejeu", func(e *core.RequestEvent) error {
			return e.String(http.StatusOK, "atteint")
		}).Bind(exigeLeJetonAntiRejeu()).Unbind("patachooPoseLeJetonAntiRejeu")
	})

	for _, cas := range []struct {
		nom    string
		champs url.Values
	}{
		{"champ absent", url.Values{}},
		{"champ vide", url.Values{nomDuChampAttendu: {""}}},
		{"champ garni", url.Values{nomDuChampAttendu: {jetonDeTest}}},
	} {
		t.Run(cas.nom, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/sonde-antirejeu",
				strings.NewReader(cas.champs.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("statut %d, attendu %d — le gestionnaire a été atteint sans jeton posé",
					rec.Code, http.StatusForbidden)
			}
		})
	}
}
