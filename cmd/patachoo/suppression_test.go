package main

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// --- Le montage -----------------------------------------------------------

// cheminDeLaSuppression rend l'adresse que les deux routes partagent.
func cheminDeLaSuppression(recette *core.Record) string {
	return "/recettes/" + recette.Id + "/supprimer"
}

// laSienne enregistre une recette signée du compte de la session : c'est le
// seul état où la suppression est offerte.
func laSienne(t *testing.T, app core.App, champs map[string]any) *core.Record {
	t.Helper()

	if champs == nil {
		champs = map[string]any{}
	}
	champs[champAuteur] = compteDeLaSession(t, app).Id
	return recetteEnregistree(t, app, champs)
}

// laRecetteEstEnBase dit si la recette est encore là. Une lecture en base, et
// non l'enregistrement gardé en mémoire : c'est ce qui est écrit qui compte.
func laRecetteEstEnBase(t *testing.T, app core.App, id string) bool {
	t.Helper()

	_, err := app.FindRecordById("recipes", id)
	return err == nil
}

// lienDeSuppression capte le lien de la fiche vers sa page de confirmation :
// ses attributs, l'identifiant qu'il vise et son libellé.
//
// L'adresse est bornée par [^"/]+ pour ne pas attraper les liens de
// suppression des notes de cuisine, qui se terminent par le même mot après un
// /commentaires/.
var lienDeSuppression = regexp.MustCompile(`<a\s([^>]*href="/recettes/([^"/]+)/supprimer"[^>]*)>([^<]*)</a>`)

// --- La page de confirmation ----------------------------------------------

// La confirmation dit ce qui part avec la recette, comptes à l'appui : les
// notes des autres comptes s'effacent avec elle, et c'est précisément ce qu'on
// ne peut pas laisser découvrir après coup.
func TestLaPageDeConfirmationAnnonceCeQuiPartAvecLaRecette(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := laSienne(t, app, map[string]any{
		"title": "Tarte aux pommes",
		"image": imageDeTest(t),
	})
	creeIngredients(t, app, recette, []string{"3 pommes", "200 g de farine"})
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	noteEnBase(t, app, recette, compteDeLaSession(t, app), "La mienne.")
	noteEnBase(t, app, recette, autre, "Celle d'un autre.")

	rec := demande(mux, cheminDeLaSuppression(recette), cookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}

	corps := rec.Body.String()
	// Le compte des notes porte sur toutes celles de la recette, pas sur les
	// seules siennes : une note sur les deux appartient à un autre compte.
	exigeContient(t, corps, "Tarte aux pommes", "2 ingrédients", "2 notes", "photo")
}

// Un libellé disparaît avec sa donnée, comme sur la fiche : une recette qui
// n'emporte rien n'affiche aucune liste, et pas trois lignes à zéro.
func TestLaPageDeConfirmationTaitCeQueLaRecetteNaPas(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := laSienne(t, app, nil)

	corps := demande(mux, cheminDeLaSuppression(recette), cookie, nil).Body.String()

	exigeSansAucun(t, corps, `class="emporte"`, "ingrédient", "note", "photo")
}

// Le titre vient d'un site tiers par l'import : il ressort échappé, comme sur
// la fiche (DOD.md §3).
func TestLeTitreEstEchappeSurLaPageDeConfirmation(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := laSienne(t, app, map[string]any{"title": `<script>alert(1)</script>`})

	corps := demande(mux, cheminDeLaSuppression(recette), cookie, nil).Body.String()

	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("le titre ressort exécutable :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("le titre ne ressort pas échappé :\n%s", corps)
	}
}

// --- La suppression --------------------------------------------------------

// La recette part, et avec elle les deux cascades du schéma : ses ingrédients
// et les notes de tous les comptes.
func TestLaSuppressionEmporteLaRecetteEtSesCascades(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := laSienne(t, app, map[string]any{"image": imageDeTest(t)})
	creeIngredients(t, app, recette, []string{"3 pommes", "200 g de farine"})
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	noteEnBase(t, app, recette, compteDeLaSession(t, app), "La mienne.")
	noteEnBase(t, app, recette, autre, "Celle d'un autre.")

	rec := poste(t, mux, cheminDeLaSuppression(recette), cookie, nil)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if lieu := rec.Header().Get("Location"); lieu != "/recettes" {
		t.Errorf("Location %q, attendu %q", lieu, "/recettes")
	}
	if laRecetteEstEnBase(t, app, recette.Id) {
		t.Errorf("la recette %s est encore en base après sa suppression", recette.Id)
	}
	if n := nombreDIngredients(t, app); n != 0 {
		t.Errorf("%d ingrédients après la suppression, 0 attendu", n)
	}
	if notes := notesDe(t, app, recette); len(notes) != 0 {
		t.Errorf("%d notes après la suppression, 0 attendue : la note d'un autre a survécu", len(notes))
	}
}

// --- Propriété : le sens du refus -----------------------------------------

// Un compte connecté qui vise la recette d'un autre obtient un 404, et non un
// 403 : l'existence d'une recette qu'on ne peut pas supprimer n'est pas une
// information à donner par un code de statut.
//
// Le contrôle se refait dans le code de la route, et pas seulement par
// DeleteRule : e.App.Delete n'applique pas les règles de collection, qui
// gardent l'API REST et non notre code Go.
func TestLesRoutesDeSuppressionRefusentLaRecetteDunAutre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	recette := recetteEnregistree(t, app, map[string]any{champAuteur: autre.Id})
	creeIngredients(t, app, recette, []string{"3 pommes"})
	noteEnBase(t, app, recette, autre, "Celle d'un autre.")

	for _, methode := range []string{http.MethodGet, http.MethodPost} {
		rec := avecCookie(mux, methode, cheminDeLaSuppression(recette), cookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s : statut %d, attendu %d", methode, rec.Code, http.StatusNotFound)
		}
	}

	if !laRecetteEstEnBase(t, app, recette.Id) {
		t.Fatalf("la recette d'un autre a été supprimée")
	}
	if n := nombreDIngredients(t, app); n != 1 {
		t.Errorf("%d ingrédients après les deux requêtes, 1 attendu", n)
	}
	if notes := notesDe(t, app, recette); len(notes) != 1 {
		t.Errorf("%d notes après les deux requêtes, 1 attendue", len(notes))
	}
}

// created_by n'est pas Required : une recette peut le porter vide, et c'est le
// cas que le premier terme de DeleteRule existe pour couvrir. Elle n'appartient
// à personne, donc personne ne la supprime depuis l'interface.
func TestUneRecetteSansAuteurNestSupprimableParPersonne(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	// created_by vide, écrit à la main : la fixture attribue par défaut à la
	// session, et c'est justement l'absence d'auteur qui est le sujet ici.
	recette := recetteEnregistree(t, app, map[string]any{champAuteur: ""})

	for _, methode := range []string{http.MethodGet, http.MethodPost} {
		rec := avecCookie(mux, methode, cheminDeLaSuppression(recette), cookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s : statut %d, attendu %d", methode, rec.Code, http.StatusNotFound)
		}
	}

	if !laRecetteEstEnBase(t, app, recette.Id) {
		t.Errorf("une recette sans auteur a été supprimée")
	}
	if corps := fiche(mux, cookie, recette.Id).Body.String(); lienDeSuppression.MatchString(corps) {
		t.Errorf("la fiche d'une recette sans auteur offre le lien de suppression :\n%s", corps)
	}
}

// --- Les cas d'échec ordinaires -------------------------------------------

// Un identifiant inconnu rend la page « Recette introuvable » en 404 : pas une
// 500, pas une page vide.
func TestUnIdentifiantInconnuRendLaPageRecetteIntrouvable(t *testing.T) {
	_, mux, cookie := carnetDeTest(t)

	for _, methode := range []string{http.MethodGet, http.MethodPost} {
		rec := avecCookie(mux, methode, "/recettes/pas-une-recette/supprimer", cookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s : statut %d, attendu %d", methode, rec.Code, http.StatusNotFound)
		}
		if corps := rec.Body.String(); !strings.Contains(corps, "Recette introuvable") {
			t.Errorf("%s : la page « Recette introuvable » n'est pas rendue :\n%s", methode, corps)
		}
	}
}

// Les deux routes sont derrière exigeUneSession, comme les quatre autres du
// formulaire : un visiteur repart sur la page de connexion, et rien n'est
// supprimé.
func TestLesRoutesDeSuppressionExigentUneSession(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := laSienne(t, app, nil)

	for _, methode := range []string{http.MethodGet, http.MethodPost} {
		rec := avecCookie(mux, methode, cheminDeLaSuppression(recette), nil)
		if rec.Code != http.StatusSeeOther {
			t.Errorf("%s : statut %d, attendu %d", methode, rec.Code, http.StatusSeeOther)
		}
		if lieu := rec.Header().Get("Location"); lieu != "/connexion" {
			t.Errorf("%s : Location %q, attendu %q", methode, lieu, "/connexion")
		}
	}

	if !laRecetteEstEnBase(t, app, recette.Id) {
		t.Errorf("la recette a été supprimée sans session")
	}
	// Le cookie n'a servi qu'à monter la session : il prouve ici que la
	// recette reste atteignable, et que le 303 vient bien de l'absence de
	// session et non d'un identifiant perdu.
	if rec := fiche(mux, cookie, recette.Id); rec.Code != http.StatusOK {
		t.Errorf("la fiche répond %d après les deux requêtes anonymes", rec.Code)
	}
}

// --- Le bouton sur la fiche ------------------------------------------------

// Le geste n'est offert qu'à l'auteur : ce que dit la règle, et cette fois la
// condition d'affichage est justifiée — l'offrir sur la recette d'un autre
// serait promettre un 404.
func TestLeLienDeSuppressionNestOffertQueSurSaPropreRecette(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	sienne := laSienne(t, app, nil)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	dAutrui := recetteEnregistree(t, app, map[string]any{champAuteur: autre.Id})

	corps := fiche(mux, cookie, sienne.Id).Body.String()
	trouves := lienDeSuppression.FindAllStringSubmatch(corps, -1)
	if len(trouves) != 1 {
		t.Fatalf("%d lien(s) de suppression sur sa propre fiche, attendu un seul :\n%s", len(trouves), corps)
	}
	attributs, vise, libelle := trouves[0][1], trouves[0][2], trouves[0][3]

	if vise != sienne.Id {
		t.Errorf("le lien vise la recette %q, attendue %q", vise, sienne.Id)
	}
	if libelle != "Supprimer" {
		t.Errorf("libellé du lien %q, attendu %q", libelle, "Supprimer")
	}
	// La confirmation est une page qu'on ouvre, pas un fragment qu'on échange.
	if strings.Contains(attributs, "hx-") {
		t.Errorf("le lien de suppression porte un attribut HTMX : %q", attributs)
	}

	if corps := fiche(mux, cookie, dAutrui.Id).Body.String(); lienDeSuppression.MatchString(corps) {
		t.Errorf("un lien de suppression est offert sur la recette d'un autre :\n%s", corps)
	}
}

// hx-confirm s'évapore sans JavaScript : le formulaire partirait quand même, et
// le garde-fou disparaîtrait exactement là où il compte. La confirmation est
// une page rendue par le serveur, et elle l'est partout.
func TestAucunGabaritNeConfirmeParHTMX(t *testing.T) {
	fichiers, err := fs.Glob(vues, "vues/*.html")
	if err != nil {
		t.Fatalf("lecture des gabarits : %v", err)
	}
	if len(fichiers) == 0 {
		t.Fatalf("aucun gabarit lu : le test ne prouverait rien")
	}

	for _, chemin := range fichiers {
		contenu, err := fs.ReadFile(vues, chemin)
		if err != nil {
			t.Fatalf("%s : %v", chemin, err)
		}
		if strings.Contains(string(contenu), "hx-confirm") {
			t.Errorf("%s porte un hx-confirm : le garde-fou disparaît sans JavaScript", chemin)
		}
	}
}
