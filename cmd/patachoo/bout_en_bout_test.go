package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	balisage "golang.org/x/net/html"
)

// Deux parcours joués sur le produit assemblé : l'installation jusqu'au premier
// compte, puis une utilisation ordinaire du carnet.
//
// Ce qu'ils ajoutent à autonomie_test.go, qui tourne déjà sur le binaire
// livrable : celui-là vérifie qu'une instance neuve répond, ceux-ci qu'on peut
// s'en servir. Tout le reste du dépôt se teste en-process, routeur monté à la
// main sur une base de test — ce qui ne dit rien de ce que vit quelqu'un qui
// installe Patachoo. Les chaînes qui ne tiennent que de bout en bout sont
// exactement celles qu'un test en-process ne peut pas voir : la migration qui
// ferme l'inscription est-elle jouée à la première installation, le cookie de
// session survit-il d'une requête à la suivante, le jeton anti-rejeu posé par
// une page est-il celui que la route suivante attend.
//
// Ils parlent au binaire en HTTP, et rien d'autre : aucun n'appelle
// brancheLesRoutes ni ne monte de routeur. Un test qui rebâtirait son propre
// montage ne vérifierait que lui-même.
//
// Aucune requête ne sort de 127.0.0.1, comme dans autonomie_test.go : la
// compilation se fait sur le cache de modules, et le serveur écoute sur la
// boucle locale. Les deux passent donc avec SANS_RESEAU=1.
//
// Ce que ces parcours ne couvriront pas, et il vaut mieux l'écrire une fois que
// le redécouvrir : l'import par URL ne peut pas être joué ici. La politique
// d'adresses de recuperation refuse la boucle locale après résolution, et sa
// seule levée — AvecExceptionDePolitique — est une option Go, hors de portée
// d'un binaire lancé en sous-processus. Ouvrir une porte dans ce filtre pour
// faire passer un test serait échanger la sécurité du produit contre une ligne
// de couverture ; l'import reste couvert par les tests en-process.

// Les comptes des deux scénarios. Le domaine .test est réservé et ne résout
// nulle part : rien ici n'a à sortir de la machine, pas même par erreur.
const (
	courrielDuSuperuser   = "chef@patachoo.test"
	motDePasseDuSuperuser = "mot-de-passe-du-chef"

	courrielDuCompte   = "cuisinier@patachoo.test"
	motDePasseDuCompte = "mot-de-passe-du-cuisinier"
	nomDuCompte        = "Camille"
)

// La recette du scénario 2, avant et après modification. Les ingrédients
// portent une ligne que l'analyseur sait découper — « 200 g de farine » — et
// c'est voulu : la fiche n'affiche l'aliment séparément que si la chaîne
// complète a tenu, du formulaire à la table des ingrédients puis au gabarit.
const (
	titreDeLaRecette    = "Crêpes de la Chandeleur"
	ingredientsSaisis   = "200 g de farine\n3 œufs\n50 cl de lait"
	instructionsSaisies = "Mélanger la farine et les œufs.\nVerser le lait peu à peu."

	titreCorrige              = "Crêpes de la Chandeleur, version sucrée"
	ingredientsSaisisCorriges = "200 g de farine\n3 œufs\n50 cl de lait\n2 cuillères à soupe de sucre"
)

func TestLInstallationVaJusquAuPremierCompte(t *testing.T) {
	repertoire := t.TempDir()
	binaire := filepath.Join(repertoire, "patachoo")
	compileLeBinaire(t, binaire)

	// L'ordre de README.md : le superutilisateur d'abord, en ligne de commande,
	// le serveur ensuite.
	lanceLaSousCommande(t, repertoire, binaire, "superuser", "upsert", courrielDuSuperuser, motDePasseDuSuperuser)

	hote, journal := lanceLeBinaire(t, repertoire, binaire)
	base := "http://" + hote
	n := nouveauNavigateur(t, base, journal)

	// Une instance neuve s'installe porte fermée. Les deux moitiés de ce
	// constat comptent : la page n'existe pas, et rien n'y mène — un lien
	// affiché sur une porte close enverrait tout le monde sur un 404.
	n.exigeLeStatut("GET /inscription, porte fermée", n.ouvre("/inscription"), http.StatusNotFound)

	connexion := n.ouvre("/connexion")
	n.exigeLeStatut("GET /connexion, porte fermée", connexion, http.StatusOK)
	n.exigeAbsentDeLaPage("GET /connexion, porte fermée", connexion, `href="/inscription"`)

	// Le superutilisateur ouvre la porte, et lui seul : toutes les règles de la
	// collection settings sont à nil (migrations/1788994542_inscription.go).
	jeton := jetonDeSuperuser(t, base)
	ouvreLInscriptionParLAPI(t, base, jeton)

	// La porte ouverte se constate des deux côtés, comme sa fermeture.
	inscription := n.ouvre("/inscription")
	n.exigeLeStatut("GET /inscription, porte ouverte", inscription, http.StatusOK)
	n.exigeDansLaPage("GET /inscription, porte ouverte", inscription,
		`action="/inscription"`, `name="email"`, `name="password"`, `name="passwordConfirm"`)
	n.exigeDansLaPage("GET /connexion, porte ouverte", n.ouvre("/connexion"), `href="/inscription"`)

	// Le formulaire soumis avec le jeton lu dans sa propre page, jamais
	// fabriqué : c'est l'anti-rejeu réel que le parcours traverse.
	cree := n.poste("/inscription", url.Values{
		champAntiRejeu:    {jetonAntiRejeuDe(t, "/inscription", inscription.Corps)},
		"email":           {courrielDuCompte},
		"name":            {nomDuCompte},
		"password":        {motDePasseDuCompte},
		"passwordConfirm": {motDePasseDuCompte},
	})
	n.exigeLaRedirection("POST /inscription", cree, http.StatusSeeOther, "/")

	// L'inscription connecte : renvoyer l'inscrit vers un second formulaire lui
	// ferait ressaisir ce qu'il vient d'écrire. La page suivante doit donc le
	// nommer.
	accueil := n.ouvre("/recettes")
	n.exigeLeStatut("GET /recettes après inscription", accueil, http.StatusOK)
	n.exigeDansLaPage("GET /recettes après inscription", accueil, "Connecté en tant que "+nomDuCompte)
}

func TestLUtilisationDuCarnetParUnCompte(t *testing.T) {
	repertoire := t.TempDir()
	binaire := filepath.Join(repertoire, "patachoo")
	compileLeBinaire(t, binaire)

	lanceLaSousCommande(t, repertoire, binaire, "superuser", "upsert", courrielDuSuperuser, motDePasseDuSuperuser)

	hote, journal := lanceLeBinaire(t, repertoire, binaire)
	base := "http://" + hote

	// Le compte est posé par le superutilisateur, et non par /inscription : ce
	// scénario-ci part d'une instance où un compte existe, et rejouer la porte
	// de l'inscription ne prouverait ici que ce que le scénario 1 prouve déjà.
	// C'est aussi le chemin que README.md décrit quand la porte reste fermée —
	// « un compte créé sans passer par elle se crée depuis /_/ ».
	creeLeCompte(t, base, jetonDeSuperuser(t, base))

	n := nouveauNavigateur(t, base, journal)

	// En visiteur : le carnet n'est pas public, et la liste renvoie à la page
	// de connexion, dont l'en-tête ne connaît personne.
	n.exigeLaRedirection("GET /recettes en visiteur", n.ouvre("/recettes"), http.StatusFound, "/connexion")

	connexion := n.ouvre("/connexion")
	n.exigeLeStatut("GET /connexion en visiteur", connexion, http.StatusOK)
	n.exigeDansLaPage("GET /connexion en visiteur", connexion, `<a href="/connexion">Connexion</a>`)
	n.exigeLaRedirection("POST /connexion", n.poste("/connexion", url.Values{
		champAntiRejeu: {jetonAntiRejeuDe(t, "/connexion", connexion.Corps)},
		"courriel":     {courrielDuCompte},
		"mot-de-passe": {motDePasseDuCompte},
	}), http.StatusSeeOther, "/")

	// Une seule connexion dans toute l'instance : POST /connexion est plafonné
	// à 5 par minute et par adresse (migrations/1788993000_debit_connexion.go),
	// et le compteur vit dans le processus serveur. Un parcours qui se
	// reconnecterait en boucle finirait par se heurter à son propre plafond.
	formulaire := n.ouvre("/recettes/nouvelle")
	n.exigeLeStatut("GET /recettes/nouvelle, connecté", formulaire, http.StatusOK)
	n.exigeDansLaPage("GET /recettes/nouvelle, connecté", formulaire, "Connecté en tant que "+nomDuCompte)

	// En multipart, comme le formulaire de recette le déclare : c'est la
	// branche que valeursSoumises prend en vrai, jeton anti-rejeu compris.
	depot := n.posteEnMultipart("/recettes", url.Values{
		champAntiRejeu: {jetonAntiRejeuDe(t, "/recettes/nouvelle", formulaire.Corps)},
		"titre":        {titreDeLaRecette},
		"ingredients":  {ingredientsSaisis},
		"instructions": {instructionsSaisies},
	})
	// La redirection porte l'identifiant : c'est par elle que le parcours
	// apprend où la recette a été rangée, exactement comme le navigateur. Une
	// saisie refusée re-rendrait le formulaire sous un 200, d'où le contrôle du
	// code avant tout le reste.
	fiche := n.ficheCreee("POST /recettes", depot)

	// Créée veut dire retrouvable : sur la liste, et sur sa propre page.
	liste := n.ouvre("/recettes")
	n.exigeLeStatut("GET /recettes après création", liste, http.StatusOK)
	n.exigeDansLaPage("GET /recettes après création", liste, titreDeLaRecette, `href="`+fiche+`"`)

	page := n.ouvre(fiche)
	n.exigeLeStatut("GET "+fiche, page, http.StatusOK)
	n.exigeDansLaPage("GET "+fiche, page,
		"<h1>"+titreDeLaRecette+"</h1>",
		`title="200 g de farine"`,
		`<span class="aliment">farine</span>`,
		"<li>Mélanger la farine et les œufs.</li>")

	// La modification : le formulaire prérempli, puis la même page corrigée.
	modification := n.ouvre(fiche + "/modifier")
	n.exigeLeStatut("GET "+fiche+"/modifier", modification, http.StatusOK)
	n.exigeDansLaPage("GET "+fiche+"/modifier", modification,
		`action="`+fiche+`"`, `value="`+titreDeLaRecette+`"`)

	n.exigeLaRedirection("POST "+fiche, n.posteEnMultipart(fiche, url.Values{
		champAntiRejeu: {jetonAntiRejeuDe(t, fiche+"/modifier", modification.Corps)},
		"titre":        {titreCorrige},
		"ingredients":  {ingredientsSaisisCorriges},
		"instructions": {instructionsSaisies},
	}), http.StatusSeeOther, fiche)

	// Le titre d'avant doit avoir disparu : une modification qui ajoute sans
	// remplacer passerait un test qui ne regarderait que le nouveau texte.
	corrigee := n.ouvre(fiche)
	n.exigeDansLaPage("GET "+fiche+" après modification", corrigee,
		"<h1>"+titreCorrige+"</h1>", `<span class="aliment">sucre</span>`)
	n.exigeAbsentDeLaPage("GET "+fiche+" après modification", corrigee, "<h1>"+titreDeLaRecette+"</h1>")

	// La déconnexion, et ce qu'elle doit refermer derrière elle.
	n.exigeLaRedirection("POST /deconnexion", n.poste("/deconnexion", url.Values{
		champAntiRejeu: {jetonAntiRejeuDe(t, fiche, corrigee.Corps)},
	}), http.StatusSeeOther, "/")

	n.exigeLaRedirection("GET /recettes/nouvelle après déconnexion",
		n.ouvre("/recettes/nouvelle"), http.StatusSeeOther, "/connexion")
}

// --- Le socle ---------------------------------------------------------------

// navigateur est ce qui tient lieu de navigateur dans les deux parcours : un
// client qui garde les cookies d'une requête à la suivante, l'adresse du
// serveur lancé, et de quoi recracher son journal quand une assertion tombe.
//
// Les cookies sont tout l'enjeu : la session et la moitié du jeton anti-rejeu
// y voyagent, et un client qui les oublierait d'une requête à l'autre ne
// jouerait aucun parcours.
type navigateur struct {
	t       *testing.T
	client  *http.Client
	base    string
	journal func() string
}

// nouveauNavigateur monte le client à cookies du parcours.
//
// net/http/cookiejar suffit, et c'est vérifié plutôt que supposé : il renvoie
// bien un cookie Secure sur http://127.0.0.1, qu'il traite comme une origine
// sûre (secureMatch, net/http/cookiejar/jar.go) — la même exception que celle
// des navigateurs, décrite dans session.go autour de cookieDeSession. Rien à
// contourner de ce côté, et surtout aucun drapeau à retirer du produit.
//
// Les redirections ne sont pas suivies, pour la raison de clientLocal : un 302
// suivi jusqu'à un 200 se lirait comme un 200 et masquerait une page déplacée.
// Les redirections attendues se vérifient sur le code et sur Location.
func nouveauNavigateur(t *testing.T, base string, journal func() string) *navigateur {
	t.Helper()

	bocal, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("impossible de monter le bocal à cookies : %v", err)
	}
	return &navigateur{
		t:    t,
		base: base,
		client: &http.Client{
			Jar:           bocal,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		journal: journal,
	}
}

// reponse porte ce qu'un parcours regarde d'une réponse.
type reponse struct {
	Statut int
	Vers   string
	Corps  string
}

// ouvre demande une page.
func (n *navigateur) ouvre(chemin string) reponse {
	n.t.Helper()
	return n.envoie("GET "+chemin, http.MethodGet, chemin, "", nil)
}

// poste soumet un formulaire tel que le navigateur l'enverrait faute d'enctype,
// c'est-à-dire en application/x-www-form-urlencoded.
func (n *navigateur) poste(chemin string, champs url.Values) reponse {
	n.t.Helper()
	return n.envoie("POST "+chemin, http.MethodPost, chemin,
		"application/x-www-form-urlencoded", strings.NewReader(champs.Encode()))
}

// posteEnMultipart soumet un formulaire en multipart/form-data, ce que déclare
// celui des recettes (vues/recette-formulaire-corps.html, à cause du champ
// image). Aucun fichier n'est joint : le parcours ne porte pas d'image.
func (n *navigateur) posteEnMultipart(chemin string, champs url.Values) reponse {
	n.t.Helper()

	corps := &bytes.Buffer{}
	redacteur := multipart.NewWriter(corps)
	for nom, valeurs := range champs {
		for _, valeur := range valeurs {
			if err := redacteur.WriteField(nom, valeur); err != nil {
				n.t.Fatalf("POST %s : écriture du champ %q : %v", chemin, nom, err)
			}
		}
	}
	if err := redacteur.Close(); err != nil {
		n.t.Fatalf("POST %s : clôture du corps multipart : %v", chemin, err)
	}
	return n.envoie("POST "+chemin, http.MethodPost, chemin, redacteur.FormDataContentType(), corps)
}

// envoie exécute la requête et lit tout le corps.
//
// Le corps est lu en entier et la réponse refermée ici : une réponse laissée
// ouverte retient sa connexion, et un parcours en fait des dizaines.
func (n *navigateur) envoie(geste, methode, chemin, typeDeCorps string, corps io.Reader) reponse {
	n.t.Helper()

	requete, err := http.NewRequest(methode, n.base+chemin, corps)
	if err != nil {
		n.t.Fatalf("%s : requête impossible à construire : %v", geste, err)
	}
	if typeDeCorps != "" {
		requete.Header.Set("Content-Type", typeDeCorps)
	}

	recue, err := n.client.Do(requete)
	if err != nil {
		n.t.Fatalf("%s n'a pas abouti : %v\n%s", geste, err, n.journal())
	}
	defer recue.Body.Close()

	lu, err := io.ReadAll(recue.Body)
	if err != nil {
		n.t.Fatalf("%s : lecture du corps : %v", geste, err)
	}
	return reponse{Statut: recue.StatusCode, Vers: recue.Header.Get("Location"), Corps: string(lu)}
}

// exigeLeStatut échoue si le code diffère de celui attendu.
func (n *navigateur) exigeLeStatut(geste string, r reponse, statut int) {
	n.t.Helper()
	if r.Statut != statut {
		n.t.Fatalf("%s a répondu %d, attendu %d\n%s", geste, r.Statut, statut, n.extrait(r))
	}
}

// exigeLaRedirection échoue si le couple code + Location n'est pas celui
// attendu. Les deux, et pas le seul code : une redirection vers ailleurs est un
// parcours cassé autrement, pas un parcours qui marche.
func (n *navigateur) exigeLaRedirection(geste string, r reponse, statut int, vers string) {
	n.t.Helper()
	if r.Statut != statut || r.Vers != vers {
		n.t.Fatalf("%s a répondu %d vers %q, attendu %d vers %q\n%s",
			geste, r.Statut, r.Vers, statut, vers, n.extrait(r))
	}
}

// exigeDansLaPage échoue si l'un des morceaux manque au corps rendu.
//
// Le corps et pas seulement le code : le registre de gabarits rend une chaîne
// vide sans la moindre erreur quand un fichier manque à l'appel (pages.go), et
// un 200 ne dit donc rien de ce que la page contient.
func (n *navigateur) exigeDansLaPage(geste string, r reponse, morceaux ...string) {
	n.t.Helper()

	var manquants []string
	for _, morceau := range morceaux {
		if !strings.Contains(r.Corps, morceau) {
			manquants = append(manquants, morceau)
		}
	}
	if manquants != nil {
		n.t.Fatalf("%s : la page ne porte pas %q\n%s", geste, manquants, n.extrait(r))
	}
}

// exigeAbsentDeLaPage échoue si l'un des morceaux se trouve dans le corps rendu.
func (n *navigateur) exigeAbsentDeLaPage(geste string, r reponse, morceaux ...string) {
	n.t.Helper()

	var presents []string
	for _, morceau := range morceaux {
		if strings.Contains(r.Corps, morceau) {
			presents = append(presents, morceau)
		}
	}
	if presents != nil {
		n.t.Fatalf("%s : la page porte %q, qui ne devrait pas y être\n%s", geste, presents, n.extrait(r))
	}
}

// ficheCreee lit dans la redirection d'une création l'adresse de la fiche.
func (n *navigateur) ficheCreee(geste string, r reponse) string {
	n.t.Helper()

	if r.Statut != http.StatusSeeOther || !strings.HasPrefix(r.Vers, "/recettes/") || r.Vers == "/recettes/" {
		n.t.Fatalf("%s a répondu %d vers %q, attendu %d vers la fiche créée\n%s",
			geste, r.Statut, r.Vers, http.StatusSeeOther, n.extrait(r))
	}
	return r.Vers
}

// extrait rassemble ce qu'il faut pour comprendre un échec sans tout refaire à
// la main : la page rendue, et ce que le serveur a écrit pendant ce temps.
func (n *navigateur) extrait(r reponse) string {
	return fmt.Sprintf("page reçue :\n%s\njournal du serveur :\n%s", r.Corps, n.journal())
}

// jetonAntiRejeuDe lit le champ caché que chaque formulaire en POST porte.
//
// Lu dans le HTML plutôt que fabriqué : un test qui poserait lui-même la valeur
// du cookie vérifierait sa propre arithmétique, quand celui-ci vérifie que la
// page et la route s'accordent — c'est-à-dire la seule chose que l'anti-rejeu
// promet.
//
// Le premier champ trouvé suffit : les formulaires d'une même page recopient
// tous la valeur rangée par poseLeJetonAntiRejeu pour cette requête-là, y
// compris celui de la déconnexion dans l'en-tête.
//
// golang.org/x/net/html plutôt qu'une expression régulière : c'est déjà une
// dépendance directe — import.go et jsonld/extraction.go s'en servent —, et un
// motif sur du HTML se trompe le jour où l'ordre des attributs change.
func jetonAntiRejeuDe(t *testing.T, page, corps string) string {
	t.Helper()

	racine, err := balisage.Parse(strings.NewReader(corps))
	if err != nil {
		t.Fatalf("%s : HTML illisible : %v", page, err)
	}

	var jeton string
	var parcours func(*balisage.Node)
	parcours = func(noeud *balisage.Node) {
		if jeton != "" {
			return
		}
		if noeud.Type == balisage.ElementNode && noeud.Data == "input" {
			var nom, valeur string
			for _, attribut := range noeud.Attr {
				switch attribut.Key {
				case "name":
					nom = attribut.Val
				case "value":
					valeur = attribut.Val
				}
			}
			if nom == champAntiRejeu {
				jeton = valeur
				return
			}
		}
		for enfant := noeud.FirstChild; enfant != nil; enfant = enfant.NextSibling {
			parcours(enfant)
		}
	}
	parcours(racine)

	if jeton == "" {
		t.Fatalf("%s ne porte aucun champ caché %q : le formulaire est mort-né\npage reçue :\n%s",
			page, champAntiRejeu, corps)
	}
	return jeton
}

// lanceLaSousCommande exécute le binaire sur une autre sous-commande que serve,
// et attend sa fin.
//
// Attendre sa fin n'est pas un détail de confort, et l'appeler avant
// lanceLeBinaire non plus : superuser upsert ouvre la même pb_data que le
// serveur, et deux processus dessus ne sont pas un scénario d'installation.
func lanceLaSousCommande(t *testing.T, repertoire, binaire string, arguments ...string) {
	t.Helper()

	cmd := exec.Command(binaire, arguments...)
	cmd.Dir = repertoire

	if sortie, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s a échoué : %v\n%s", filepath.Base(binaire), strings.Join(arguments, " "), err, sortie)
	}
}

// jetonDeSuperuser rejoue ce que donne INSTALL.md, section « Exposer Patachoo
// hors de chez soi » : une authentification sur la collection _superusers, dont
// sort le jeton qui ouvre l'administration.
//
// clientLocal et non le bocal du parcours : la requête porte son jeton dans
// Authorization, et la mêler à la session du navigateur brouillerait ce que
// chaque étape prouve. C'est d'ailleurs ce que le produit lui-même refuse —
// recopieLeCookieDansLEnTete s'arrête aux chemins qui ne sont pas /api/.
func jetonDeSuperuser(t *testing.T, base string) string {
	t.Helper()

	var recu struct {
		Token string `json:"token"`
	}
	appelleLAPI(t, base, http.MethodPost, "/api/collections/_superusers/auth-with-password", "",
		map[string]string{"identity": courrielDuSuperuser, "password": motDePasseDuSuperuser}, &recu)

	if recu.Token == "" {
		t.Fatal("authentification du superutilisateur : aucun jeton rendu")
	}
	return recu.Token
}

// ouvreLInscriptionParLAPI bascule open_registration sur l'unique enregistrement de la
// collection settings.
//
// Par l'API et avec le jeton de superutilisateur, faute d'autre chemin : toutes
// les règles de settings sont à nil (migrations/1788994542_inscription.go), et
// le produit n'offre aucune page de réglages. C'est exactement le geste que
// README.md décrit, « collection settings, l'unique enregistrement, case
// open_registration ».
func ouvreLInscriptionParLAPI(t *testing.T, base, jeton string) {
	t.Helper()

	var reglages struct {
		Items []struct {
			Id string `json:"id"`
		} `json:"items"`
	}
	appelleLAPI(t, base, http.MethodGet, "/api/collections/settings/records", jeton, nil, &reglages)

	if len(reglages.Items) != 1 {
		t.Fatalf("la collection settings porte %d enregistrements, attendu 1", len(reglages.Items))
	}
	appelleLAPI(t, base, http.MethodPatch, "/api/collections/settings/records/"+reglages.Items[0].Id, jeton,
		map[string]bool{"open_registration": true}, nil)
}

// creeLeCompte pose le compte du scénario 2 depuis le jeton de
// superutilisateur.
//
// users.createRule est verrouillée au superuser depuis la même migration :
// l'inscription n'a qu'une porte, la nôtre, et un compte créé sans passer par
// elle se crée comme ici.
func creeLeCompte(t *testing.T, base, jeton string) {
	t.Helper()

	appelleLAPI(t, base, http.MethodPost, "/api/collections/users/records", jeton, map[string]string{
		"email":           courrielDuCompte,
		"password":        motDePasseDuCompte,
		"passwordConfirm": motDePasseDuCompte,
		"name":            nomDuCompte,
	}, nil)
}

// appelleLAPI parle à l'API REST de PocketBase en JSON et décode la réponse
// dans rendu, quand il y en a un à décoder.
//
// Sans préfixe Bearer sur le jeton : c'est la forme qu'INSTALL.md donne, et
// celle que getAuthTokenFromRequest accepte.
func appelleLAPI(t *testing.T, base, methode, chemin, jeton string, envoye, rendu any) {
	t.Helper()

	var corps io.Reader
	if envoye != nil {
		encode, err := json.Marshal(envoye)
		if err != nil {
			t.Fatalf("%s %s : corps inencodable : %v", methode, chemin, err)
		}
		corps = bytes.NewReader(encode)
	}

	requete, err := http.NewRequest(methode, base+chemin, corps)
	if err != nil {
		t.Fatalf("%s %s : requête impossible à construire : %v", methode, chemin, err)
	}
	requete.Header.Set("Content-Type", "application/json")
	if jeton != "" {
		requete.Header.Set("Authorization", jeton)
	}

	recue, err := clientLocal.Do(requete)
	if err != nil {
		t.Fatalf("%s %s n'a pas abouti : %v", methode, chemin, err)
	}
	defer recue.Body.Close()

	lu, err := io.ReadAll(recue.Body)
	if err != nil {
		t.Fatalf("%s %s : lecture du corps : %v", methode, chemin, err)
	}
	if recue.StatusCode < 200 || recue.StatusCode >= 300 {
		t.Fatalf("%s %s a répondu %d\nréponse :\n%s", methode, chemin, recue.StatusCode, lu)
	}

	if rendu != nil {
		if err := json.Unmarshal(lu, rendu); err != nil {
			t.Fatalf("%s %s : réponse illisible : %v\nréponse :\n%s", methode, chemin, err, lu)
		}
	}
}
