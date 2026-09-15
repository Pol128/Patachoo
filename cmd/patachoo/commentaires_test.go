package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// --- Montage ---------------------------------------------------------------

// posteNote envoie le formulaire des notes en urlencodé, qui est ce qu'envoie
// un formulaire sans <input type="file"> : le poste de recettes_test.go part en
// multipart et exercerait un décodage que ce formulaire-ci n'emprunte jamais.
func posteNote(t *testing.T, mux http.Handler, cible string, cookie *http.Cookie, champs url.Values, entetes map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, cible, strings.NewReader(leJetonEstPose(champs).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookieDuJetonDeTest())
	for nom, valeur := range entetes {
		req.Header.Set(nom, valeur)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// corpsDe rend le champ du formulaire d'ajout ou d'édition, tel qu'un
// navigateur le poste.
func corpsDe(texte string) url.Values { return url.Values{"corps": {texte}} }

// compteDeLaSession rend le compte que carnetDeTest a connecté.
func compteDeLaSession(t *testing.T, app core.App) *core.Record {
	t.Helper()

	compte, err := app.FindAuthRecordByEmail("users", courrielDeTest)
	if err != nil {
		t.Fatalf("compte de la session : %v", err)
	}
	return compte
}

// noteEnBase écrit une note directement, sans passer par la route : c'est
// l'état de départ des tests d'affichage, d'édition et de propriété.
//
// created et updated se posent par SetRaw, comme les dates de creeRecette : le
// champ est un autodate, et PocketBase ne respecte une date choisie que par là
// (core/field_autodate.go). Sans ça, trois notes écrites dans la même
// milliseconde sortiraient dans un ordre que rien ne fixe.
func noteEnBase(t *testing.T, app core.App, recette, auteur *core.Record, corps string, dates ...time.Time) *core.Record {
	t.Helper()

	collection, err := app.FindCollectionByNameOrId("comments")
	if err != nil {
		t.Fatalf("collection comments : %v", err)
	}

	note := core.NewRecord(collection)
	note.Set("recipe", recette.Id)
	note.Set("author", auteur.Id)
	note.Set("body", corps)
	for i, quand := range dates {
		date, err := types.ParseDateTime(quand)
		if err != nil {
			t.Fatalf("date %v : %v", quand, err)
		}
		note.SetRaw([]string{"created", "updated"}[i], date)
	}
	if err := app.Save(note); err != nil {
		t.Fatalf("enregistrement de la note %q : %v", corps, err)
	}
	return note
}

// notesDe rend les notes d'une recette, telles que la base les porte.
func notesDe(t *testing.T, app core.App, recette *core.Record) []*core.Record {
	t.Helper()

	notes, err := app.FindAllRecords("comments", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		t.Fatalf("lecture des notes : %v", err)
	}
	return notes
}

// relitLaNote relit une note en base, ou fait échouer le test : une assertion
// sur un enregistrement gardé en mémoire ne dirait rien de ce qui est écrit.
func relitLaNote(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()

	note, err := app.FindRecordById("comments", id)
	if err != nil {
		t.Fatalf("relecture de la note %s : %v", id, err)
	}
	return note
}

// routeDesNotes décrit une des quatre routes, pour les tests qui les
// parcourent toutes.
type routeDesNotes struct {
	nom     string
	methode string
	cible   string
}

// lesQuatreRoutes rend les quatre chemins de la tâche, dans l'ordre du
// déroulé : ajouter, ouvrir le formulaire d'édition, enregistrer, supprimer.
func lesQuatreRoutes(recette, note *core.Record) []routeDesNotes {
	base := "/recettes/" + recette.Id + "/commentaires"
	return []routeDesNotes{
		{"ajout", http.MethodPost, base},
		{"formulaire d'édition", http.MethodGet, base + "/" + note.Id + "/modifier"},
		{"modification", http.MethodPost, base + "/" + note.Id},
		{"suppression", http.MethodPost, base + "/" + note.Id + "/supprimer"},
	}
}

// joue exécute une route, avec ou sans session, avec ou sans en-têtes.
func joue(t *testing.T, mux http.Handler, r routeDesNotes, cookie *http.Cookie, entetes map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	if r.methode == http.MethodGet {
		return demande(mux, r.cible, cookie, entetes)
	}
	return posteNote(t, mux, r.cible, cookie, corpsDe("Trop cuit de dix minutes."), entetes)
}

// --- Session : le sens du refus -------------------------------------------

// Le contrôle de session passe avant la recherche de la recette et avant la
// validation du formulaire : sans session, rien n'est rendu, rien n'est écrit,
// la requête part vers la page de connexion.
func TestLesQuatreRoutesDeNotesRefusentUnVisiteur(t *testing.T) {
	app, mux, _ := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Trop cuit.")

	for _, r := range lesQuatreRoutes(recette, note) {
		t.Run(r.nom, func(t *testing.T) {
			rec := joue(t, mux, r, nil, nil)

			if rec.Code != http.StatusSeeOther {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
			}
			if destination := rec.Header().Get("Location"); destination != "/connexion" {
				t.Errorf("Location %q, attendu %q", destination, "/connexion")
			}
			if corps := rec.Body.String(); strings.Contains(corps, "Trop cuit.") {
				t.Errorf("une note a été rendue à un visiteur :\n%s", corps)
			}
		})
	}
}

// Un refus qui répondrait bien mais écrirait quand même ne protégerait rien :
// les trois POST se vérifient sur la base, pas sur la réponse.
func TestSansSessionAucuneNoteNeBouge(t *testing.T) {
	app, mux, _ := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Texte d'origine.")

	base := "/recettes/" + recette.Id + "/commentaires"
	posteNote(t, mux, base, nil, corpsDe("Note injectée."), nil)
	posteNote(t, mux, base+"/"+note.Id, nil, corpsDe("Texte injecté."), nil)
	posteNote(t, mux, base+"/"+note.Id+"/supprimer", nil, nil, nil)

	notes := notesDe(t, app, recette)
	if len(notes) != 1 {
		t.Fatalf("%d notes après trois POST sans session, 1 attendue", len(notes))
	}
	if corps := relitLaNote(t, app, note.Id).GetString("body"); corps != "Texte d'origine." {
		t.Errorf("corps %q après une modification sans session, %q attendu", corps, "Texte d'origine.")
	}
}

// Le sens de l'autorisation, sans quoi le critère précédent serait satisfait
// par quatre routes cassées.
func TestLesQuatreRoutesDeNotesServentUnCompteConnecte(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	base := "/recettes/" + recette.Id + "/commentaires"

	if rec := posteNote(t, mux, base, cookie, corpsDe("Trop cuit."), nil); rec.Code != http.StatusSeeOther {
		t.Errorf("ajout : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if n := len(notesDe(t, app, recette)); n != 1 {
		t.Fatalf("%d notes après un ajout, 1 attendue", n)
	}
	note := notesDe(t, app, recette)[0]

	if rec := demande(mux, base+"/"+note.Id+"/modifier", cookie, nil); rec.Code != http.StatusOK {
		t.Errorf("formulaire d'édition : statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if rec := posteNote(t, mux, base+"/"+note.Id, cookie, corpsDe("Parfait à 180 °C."), nil); rec.Code != http.StatusSeeOther {
		t.Errorf("modification : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if corps := relitLaNote(t, app, note.Id).GetString("body"); corps != "Parfait à 180 °C." {
		t.Errorf("corps %q après la modification, %q attendu", corps, "Parfait à 180 °C.")
	}

	if rec := posteNote(t, mux, base+"/"+note.Id+"/supprimer", cookie, nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("suppression : statut %d, attendu %d", rec.Code, http.StatusSeeOther)
	}
	if n := len(notesDe(t, app, recette)); n != 0 {
		t.Errorf("%d notes après la suppression, 0 attendue", n)
	}
}

// --- Propriété : le sens du refus -----------------------------------------

// Un compte connecté qui vise la note d'un autre obtient un 404, pas un 403 :
// l'existence d'une note d'autrui n'est pas une information à donner par un
// code de statut. La propriété se vérifie dans le code de la route, et pas
// seulement par la règle de collection — ces routes-là sont servies par notre
// code Go, que les règles ne gardent pas.
func TestLesRoutesDeNoteRefusentLaNoteDunAutre(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")
	note := noteEnBase(t, app, recette, autre, "Texte d'origine.")

	// La première route ne vise aucune note : elle est couverte ailleurs.
	for _, r := range lesQuatreRoutes(recette, note)[1:] {
		t.Run(r.nom, func(t *testing.T) {
			rec := joue(t, mux, r, cookie, nil)

			if rec.Code != http.StatusNotFound {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusNotFound)
			}
		})
	}

	notes := notesDe(t, app, recette)
	if len(notes) != 1 {
		t.Fatalf("%d notes après les trois requêtes, 1 attendue", len(notes))
	}
	relue := relitLaNote(t, app, note.Id)
	if corps := relue.GetString("body"); corps != "Texte d'origine." {
		t.Errorf("corps %q, %q attendu : la note d'un autre a été modifiée", corps, "Texte d'origine.")
	}
	if auteur := relue.GetString("author"); auteur != autre.Id {
		t.Errorf("auteur %q, %q attendu", auteur, autre.Id)
	}
}

// --- L'ajout ---------------------------------------------------------------

func TestUnAjoutValideCreeLaNoteSigneeDuCompteConnecte(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	posteNote(t, mux, "/recettes/"+recette.Id+"/commentaires", cookie,
		corpsDe("J'ai fait cette recette hier, le temps de cuisson ne va pas."), nil)

	notes := notesDe(t, app, recette)
	if len(notes) != 1 {
		t.Fatalf("%d notes après l'ajout, 1 attendue", len(notes))
	}
	note := notes[0]
	if auteur, attendu := note.GetString("author"), compteDeLaSession(t, app).Id; auteur != attendu {
		t.Errorf("author %q, %q attendu", auteur, attendu)
	}
	if plat := note.GetString("recipe"); plat != recette.Id {
		t.Errorf("recipe %q, %q attendu", plat, recette.Id)
	}
	if corps := note.GetString("body"); corps != "J'ai fait cette recette hier, le temps de cuisson ne va pas." {
		t.Errorf("body %q, le texte soumis attendu", corps)
	}
}

// Même règle que created_by sur les recettes : l'auteur vient de la session,
// jamais de la requête. Un champ posté à ce nom n'a nulle part où atterrir.
func TestLAuteurNestJamaisLuDeLaRequete(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")

	champs := corpsDe("Trop cuit.")
	champs.Set("author", autre.Id)
	champs.Set("auteur", autre.Id)
	posteNote(t, mux, "/recettes/"+recette.Id+"/commentaires", cookie, champs, nil)

	notes := notesDe(t, app, recette)
	if len(notes) != 1 {
		t.Fatalf("%d notes après l'ajout, 1 attendue", len(notes))
	}
	if auteur, attendu := notes[0].GetString("author"), compteDeLaSession(t, app).Id; auteur != attendu {
		t.Errorf("author %q, %q attendu : l'auteur a été lu de la requête", auteur, attendu)
	}

	// L'autre moitié du critère : le formulaire rendu ne porte aucun champ de
	// ce nom, donc rien n'invite à en poster un.
	corps := fiche(mux, cookie, recette.Id).Body.String()
	for _, nom := range []string{`name="author"`, `name="auteur"`} {
		if strings.Contains(corps, nom) {
			t.Errorf("le formulaire porte un champ %s :\n%s", nom, corps)
		}
	}
}

// --- La liste --------------------------------------------------------------

func TestLesNotesSortentDeLaPlusRecenteALaPlusAncienne(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	compte := compteDeLaSession(t, app)

	noteEnBase(t, app, recette, compte, "La plus ancienne.", instant(1))
	noteEnBase(t, app, recette, compte, "Celle du milieu.", instant(2))
	noteEnBase(t, app, recette, compte, "La plus récente.", instant(3))

	corps := fiche(mux, cookie, recette.Id).Body.String()

	recente := strings.Index(corps, "La plus récente.")
	milieu := strings.Index(corps, "Celle du milieu.")
	ancienne := strings.Index(corps, "La plus ancienne.")
	if recente < 0 || milieu < 0 || ancienne < 0 {
		t.Fatalf("les trois notes ne sont pas toutes rendues :\n%s", corps)
	}
	if !(recente < milieu && milieu < ancienne) {
		t.Errorf("ordre rendu %d/%d/%d, la plus récente attendue en premier :\n%s",
			recente, milieu, ancienne, corps)
	}
}

func TestLesNotesDuneAutreRecetteNapparaissentPas(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	compte := compteDeLaSession(t, app)
	celleCi := recetteEnregistree(t, app, map[string]any{"title": "Tarte aux pommes"})
	celleLa := recetteEnregistree(t, app, map[string]any{"title": "Poulet rôti"})

	noteEnBase(t, app, celleCi, compte, "Note de la tarte.")
	noteEnBase(t, app, celleLa, compte, "Note du poulet.")

	corps := fiche(mux, cookie, celleCi.Id).Body.String()
	if !strings.Contains(corps, "Note de la tarte.") {
		t.Errorf("la note de la recette affichée manque :\n%s", corps)
	}
	if strings.Contains(corps, "Note du poulet.") {
		t.Errorf("la note d'une autre recette apparaît :\n%s", corps)
	}
}

// Le nom de l'auteur, c'est users.name — et le schéma de PocketBase permet
// qu'il soit vide.
func TestUneNoteSigneParSonAuteurOuParLeRepli(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	nomme := creeCompte(t, app, "marguerite@exemple.fr", "Marguerite")
	anonyme := creeCompte(t, app, "sans-nom@exemple.fr", "")

	noteEnBase(t, app, recette, nomme, "Note signée.", instant(2))
	noteEnBase(t, app, recette, anonyme, "Note sans nom.", instant(1))

	corps := fiche(mux, cookie, recette.Id).Body.String()
	if !strings.Contains(corps, "Marguerite") {
		t.Errorf("le nom de l'auteur n'est pas rendu :\n%s", corps)
	}
	if !strings.Contains(corps, "Compte sans nom") {
		t.Errorf("le repli d'un compte sans nom n'est pas rendu :\n%s", corps)
	}
}

// L'adresse électronique est une donnée personnelle que emailVisibility
// protège par défaut, et users se ferme sur elle-même : la contourner par
// notre rendu serveur serait une régression silencieuse. La chaîne est
// cherchée dans la réponse entière, attributs compris.
func TestLadresseDeLauteurNapparaitNullePart(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	const courrielDeLAuteur = "auteur.reconnaissable@exemple.fr"
	auteur := creeCompte(t, app, courrielDeLAuteur, "Marguerite")
	noteEnBase(t, app, recette, auteur, "Trop cuit de dix minutes.")

	corps := fiche(mux, cookie, recette.Id).Body.String()
	// La note est bien rendue : sans cette moitié-là, l'assertion passerait
	// aussi sur une fiche qui n'afficherait aucune note.
	if !strings.Contains(corps, "Trop cuit de dix minutes.") {
		t.Fatalf("la note n'est pas rendue :\n%s", corps)
	}
	if strings.Contains(corps, courrielDeLAuteur) {
		t.Errorf("le courriel de l'auteur d'une note est publié :\n%s", corps)
	}
}

// Un journal d'expérience réécrit sans le dire ne vaut plus rien. Une minute
// d'écart, et non zéro : created et updated sont posées par deux horloges
// séparées de quelques microsecondes à la création.
func TestUneNoteReecriteLeDit(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	compte := compteDeLaSession(t, app)

	ecrite := time.Date(2026, time.August, 19, 10, 0, 0, 0, time.UTC)
	noteEnBase(t, app, recette, compte, "Jamais retouchée.", ecrite, ecrite)
	noteEnBase(t, app, recette, compte, "Retouchée depuis.", ecrite, ecrite.Add(2*time.Minute))

	corps := fiche(mux, cookie, recette.Id).Body.String()
	if compte := strings.Count(corps, "modifié le"); compte != 1 {
		t.Errorf("%d mentions « modifié le », 1 attendue :\n%s", compte, corps)
	}
	if !strings.Contains(corps, "19 août 2026") {
		t.Errorf("la date n'est pas rendue en français :\n%s", corps)
	}
}

// body est du texte : un paragraphe par ligne non vide, exactement le
// traitement d'instructions tranché le 19/08/2026.
func TestLeCorpsRendUnParagrapheParLigneNonVide(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	noteEnBase(t, app, recette, compteDeLaSession(t, app),
		"Première ligne.\n\nDeuxième ligne.\nTroisième ligne.")

	corps := fiche(mux, cookie, recette.Id).Body.String()
	for _, ligne := range []string{"Première ligne.", "Deuxième ligne.", "Troisième ligne."} {
		if !strings.Contains(corps, "<p>"+ligne+"</p>") {
			t.Errorf("la ligne %q n'est pas rendue en paragraphe :\n%s", ligne, corps)
		}
	}
	if strings.Contains(corps, "<p></p>") {
		t.Errorf("une ligne vide a produit un paragraphe vide :\n%s", corps)
	}
}

// « Du texte, pas du HTML », dans sa formulation binaire : les balises
// stockées s'affichent, elles ne s'exécutent pas.
func TestLeCorpsEstDuTexteEtNonDuHTML(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	noteEnBase(t, app, recette, compteDeLaSession(t, app), "Battre en <b>gras</b>.")

	corps := fiche(mux, cookie, recette.Id).Body.String()
	if strings.Contains(corps, "<b>gras</b>") {
		t.Errorf("le corps ressort interprété comme du HTML :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;b&gt;gras&lt;/b&gt;") {
		t.Errorf("le corps ne ressort pas échappé :\n%s", corps)
	}
}

// Le texte et le nom sont deux chemins de rendu différents : un test par
// emplacement (DOD.md §3).
func TestLaListeDeNotesEchappeLeTexteEtLeNom(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	auteur := creeCompte(t, app, "malin@exemple.fr", `"><script>`)

	noteEnBase(t, app, recette, auteur, "<script>alert(1)</script>")

	corps := fiche(mux, cookie, recette.Id).Body.String()
	if strings.Contains(corps, "<script>alert(1)</script>") {
		t.Errorf("le texte de la note ressort en balise vivante :\n%s", corps)
	}
	if strings.Contains(corps, `"><script>`) {
		t.Errorf("le nom de l'auteur referme un attribut :\n%s", corps)
	}
	if !strings.Contains(corps, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("le texte de la note ne ressort pas échappé :\n%s", corps)
	}
}

// Les liens d'action ne s'affichent que sur ses propres notes : proposer à un
// compte de modifier la note d'un autre serait lui promettre un 404.
func TestLesActionsNapparaissentQueSurSesPropresNotes(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	autre := creeCompte(t, app, "autre@exemple.fr", "Autre")

	sienne := noteEnBase(t, app, recette, compteDeLaSession(t, app), "La mienne.", instant(2))
	dAutrui := noteEnBase(t, app, recette, autre, "Celle d'un autre.", instant(1))

	corps := fiche(mux, cookie, recette.Id).Body.String()
	base := "/recettes/" + recette.Id + "/commentaires/"
	if !strings.Contains(corps, base+sienne.Id+"/modifier") {
		t.Errorf("aucun lien de modification sur sa propre note :\n%s", corps)
	}
	if !strings.Contains(corps, base+sienne.Id+"/supprimer") {
		t.Errorf("aucun lien de suppression sur sa propre note :\n%s", corps)
	}
	if strings.Contains(corps, base+dAutrui.Id+"/modifier") {
		t.Errorf("un lien de modification est offert sur la note d'un autre :\n%s", corps)
	}
	if strings.Contains(corps, base+dAutrui.Id+"/supprimer") {
		t.Errorf("un lien de suppression est offert sur la note d'un autre :\n%s", corps)
	}
}

// Aucun commentaire : pas de liste vide dans le HTML. Le formulaire d'ajout,
// lui, est toujours là.
func TestUneRecetteSansNoteNaPasDeListeMaisGardeLeFormulaire(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	corps := fiche(mux, cookie, recette.Id).Body.String()
	if strings.Contains(corps, `<ul class="notes"`) {
		t.Errorf("une liste de notes vide est rendue :\n%s", corps)
	}
	if !strings.Contains(corps, `action="/recettes/`+recette.Id+`/commentaires"`) {
		t.Errorf("le formulaire d'ajout manque :\n%s", corps)
	}
}

// --- Le formulaire d'édition ----------------------------------------------

// La route 2 rend la liste avec ce commentaire-là remplacé par un formulaire
// d'édition : les autres notes restent lisibles autour.
func TestLeFormulaireDeditionRemplaceLaNoteVisee(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	compte := compteDeLaSession(t, app)

	modifiee := noteEnBase(t, app, recette, compte, "Texte à corriger.", instant(2))
	noteEnBase(t, app, recette, compte, "Texte laissé tel quel.", instant(1))

	corps := demande(mux, "/recettes/"+recette.Id+"/commentaires/"+modifiee.Id+"/modifier", cookie, nil).Body.String()

	if !strings.Contains(corps, `action="/recettes/`+recette.Id+`/commentaires/`+modifiee.Id+`"`) {
		t.Errorf("le formulaire d'édition ne poste pas sur la note visée :\n%s", corps)
	}
	if !strings.Contains(corps, ">Texte à corriger.</textarea>") {
		t.Errorf("le texte à corriger n'est pas dans le formulaire :\n%s", corps)
	}
	if !strings.Contains(corps, "Texte laissé tel quel.") {
		t.Errorf("les autres notes ont disparu :\n%s", corps)
	}
}

// --- La modification et la suppression ------------------------------------

func TestUneModificationNeChangeQueLeTexte(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	compte := compteDeLaSession(t, app)
	note := noteEnBase(t, app, recette, compte, "Texte d'origine.")
	// La date est relue en base, et non prise sur l'enregistrement en mémoire :
	// SQLite la stocke à la milliseconde, l'horloge de l'autodate la pose à la
	// nanoseconde, et les deux ne seraient jamais égales.
	cree := relitLaNote(t, app, note.Id).GetDateTime("created")

	posteNote(t, mux, "/recettes/"+recette.Id+"/commentaires/"+note.Id, cookie,
		corpsDe("Texte corrigé."), nil)

	relue := relitLaNote(t, app, note.Id)
	if corps := relue.GetString("body"); corps != "Texte corrigé." {
		t.Errorf("body %q, %q attendu", corps, "Texte corrigé.")
	}
	if auteur := relue.GetString("author"); auteur != compte.Id {
		t.Errorf("author %q, %q attendu", auteur, compte.Id)
	}
	if plat := relue.GetString("recipe"); plat != recette.Id {
		t.Errorf("recipe %q, %q attendu", plat, recette.Id)
	}
	if !relue.GetDateTime("created").Equal(cree) {
		t.Errorf("created %v, %v attendu", relue.GetDateTime("created"), cree)
	}
}

func TestUneSuppressionRetireLaNoteDeLaBaseEtDeLaFiche(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Note à retirer.")

	posteNote(t, mux, "/recettes/"+recette.Id+"/commentaires/"+note.Id+"/supprimer", cookie, nil, nil)

	if n := len(notesDe(t, app, recette)); n != 0 {
		t.Errorf("%d notes après la suppression, 0 attendue", n)
	}
	if corps := fiche(mux, cookie, recette.Id).Body.String(); strings.Contains(corps, "Note à retirer.") {
		t.Errorf("la note supprimée est encore rendue :\n%s", corps)
	}
}

// --- La saisie refusée -----------------------------------------------------

// Rien n'est écrit, le formulaire est re-rendu avec le texte déjà saisi et un
// message en français. Pas de 500, et l'utilisateur ne retape rien.
func TestUneNoteVideOuTropLongueNecritRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)

	cas := []struct {
		nom    string
		saisi  string
		temoin string
	}{
		{"vide", "", ""},
		{"des espaces", "   \n  \t ", ""},
		{"au-delà de 5000 caractères", "Trop long : " + strings.Repeat("é", 5000), "Trop long : "},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			rec := posteNote(t, mux, "/recettes/"+recette.Id+"/commentaires", cookie, corpsDe(c.saisi), nil)

			if rec.Code != http.StatusOK {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
			}
			if n := len(notesDe(t, app, recette)); n != 0 {
				t.Fatalf("%d notes après une saisie refusée, 0 attendue", n)
			}
			corps := rec.Body.String()
			if messageDErreur(t, corps) == "" {
				t.Errorf("aucun message d'erreur rendu :\n%s", corps)
			}
			if c.temoin != "" && !strings.Contains(corps, c.temoin) {
				t.Errorf("le texte saisi n'est pas re-proposé :\n%s", corps)
			}
		})
	}
}

// Une modification refusée ne touche pas non plus à l'enregistrement.
func TestUneModificationVideNeChangeRien(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Texte d'origine.")

	rec := posteNote(t, mux, "/recettes/"+recette.Id+"/commentaires/"+note.Id, cookie, corpsDe("   "), nil)

	if rec.Code != http.StatusOK {
		t.Errorf("statut %d, attendu %d", rec.Code, http.StatusOK)
	}
	if corps := relitLaNote(t, app, note.Id).GetString("body"); corps != "Texte d'origine." {
		t.Errorf("body %q après une modification refusée, %q attendu", corps, "Texte d'origine.")
	}
	if messageDErreur(t, rec.Body.String()) == "" {
		t.Errorf("aucun message d'erreur rendu :\n%s", rec.Body.String())
	}
}

// --- Page ou fragment ------------------------------------------------------

// Avec HX-Request, les quatre routes rendent le fragment des notes seul : une
// page complète renvoyée dans un hx-target produit des pages imbriquées.
func TestLesQuatreRoutesRendentLeFragmentAHTMX(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	entetes := map[string]string{"HX-Request": "true"}

	// Les quatre chemins sont calculés une fois : la suppression passe en
	// dernier, et emporte la note que les deux cas précédents ont visée.
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Trop cuit.")
	for _, r := range lesQuatreRoutes(recette, note) {
		t.Run(r.nom, func(t *testing.T) {
			rec := joue(t, mux, r, cookie, entetes)

			corps := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
			}
			if strings.TrimSpace(corps) == "" {
				t.Error("fragment vide")
			}
			if strings.Contains(corps, "<html") || strings.Contains(corps, "<body") {
				t.Errorf("document complet rendu à HTMX :\n%s", corps)
			}
		})
	}
}

// Sans l'en-tête, l'ajout, la modification et la suppression fonctionnent
// quand même : HTMX ne fait qu'éviter le rechargement.
func TestSansHTMXLesPostRedirigentEtLeGetRendLaPageEntiere(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Trop cuit.")

	for _, r := range lesQuatreRoutes(recette, note) {
		t.Run(r.nom, func(t *testing.T) {
			rec := joue(t, mux, r, cookie, nil)
			corps := rec.Body.String()

			if r.methode == http.MethodGet {
				if rec.Code != http.StatusOK {
					t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusOK)
				}
				if !strings.Contains(corps, "<html") {
					t.Errorf("document incomplet hors HTMX :\n%s", corps)
				}
				return
			}

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("statut %d, attendu %d", rec.Code, http.StatusSeeOther)
			}
			if destination := rec.Header().Get("Location"); destination != "/recettes/"+recette.Id {
				t.Errorf("Location %q, attendu %q", destination, "/recettes/"+recette.Id)
			}
		})
	}
}

// --- La recette inconnue ---------------------------------------------------

// Une recette inconnue rend la 404 de la fiche, pas une 500 — et une écriture
// sur cet identifiant n'invente pas la recette manquante.
func TestLesQuatreRoutesSurUneRecetteInconnueRepondent404(t *testing.T) {
	app, mux, cookie := carnetDeTest(t)
	recette := recetteEnregistree(t, app, nil)
	note := noteEnBase(t, app, recette, compteDeLaSession(t, app), "Trop cuit.")

	for _, r := range lesQuatreRoutes(recette, note) {
		t.Run(r.nom, func(t *testing.T) {
			cible := strings.Replace(r.cible, "/recettes/"+recette.Id, "/recettes/inexistante", 1)
			rec := joue(t, mux, routeDesNotes{r.nom, r.methode, cible}, cookie, nil)

			if rec.Code != http.StatusNotFound {
				t.Errorf("statut %d, attendu %d", rec.Code, http.StatusNotFound)
			}
		})
	}

	if n := len(notesDe(t, app, recette)); n != 1 {
		t.Errorf("%d notes après quatre requêtes sur une recette inconnue, 1 attendue", n)
	}
}
