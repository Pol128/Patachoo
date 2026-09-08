package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"github.com/pocketbase/pocketbase/tools/types"
)

// corpsMaxDUneNote borne ce qu'une note accepte, en caractères.
//
// La même valeur que celle posée sur le schéma par la migration, et pour la
// même raison (DOD.md §3, « Limites »). Le doublon est assumé : le schéma
// refuse, la route explique — un utilisateur qui dépasse doit lire une phrase
// française, pas l'erreur de validation de PocketBase.
const corpsMaxDUneNote = 5000

// nomDeReplietDeLAuteur s'affiche quand users.name est vide, ce que le schéma
// de PocketBase permet. L'adresse électronique n'est jamais rendue à sa place :
// c'est une donnée personnelle que emailVisibility protège par défaut, et la
// publier par notre rendu serveur serait une régression silencieuse.
const nomDeReplietDeLAuteur = "Compte sans nom"

// ecartDeReecriture : en deçà, updated et created décrivent la même écriture.
//
// created et updated sont posées par deux appels d'horloge séparés de quelques
// microsecondes à la création : comparer les deux dates à l'égalité ferait
// porter « modifié le » à toutes les notes.
const ecartDeReecriture = time.Minute

// erreurIntrouvable marque ce qui doit devenir la 404 de la fiche : une recette
// qui n'existe pas, ou une note qui n'appartient pas au compte connecté.
//
// Une valeur remontée par la route plutôt qu'un rendu fait sur place : le choix
// entre document et fragment appartient à rendre, et une fonction de recherche
// qui écrirait la réponse ne pourrait plus être appelée deux fois.
var erreurIntrouvable = errors.New("introuvable")

// donneesCommentaires porte le bloc des notes tel que le gabarit le lit.
//
// Tout y est déjà mis en forme — dates en français, paragraphes découpés,
// droits résolus — parce que le gabarit n'a pas de quoi le faire.
type donneesCommentaires struct {
	RecetteId string
	Notes     []noteAffichee

	// Message et Brouillon ne servent qu'au refus d'une saisie : le message en
	// français, et le texte déjà tapé, pour que l'utilisateur ne retape rien.
	Message   string
	Brouillon string
}

// noteAffichee est une note telle qu'elle se lit sous la recette.
type noteAffichee struct {
	Id     string
	Auteur string
	Date   string

	// Modifiee vaut "" tant que la note n'a pas été réécrite : un journal
	// d'expérience réécrit sans le dire ne vaut plus rien.
	Modifiee string

	// Paragraphes découpe le texte, une entrée par ligne non vide. Du texte,
	// jamais du HTML : le même traitement qu'instructions.
	Paragraphes []string

	// Sienne dit que la note est celle du compte connecté, et commande les
	// liens d'action : les offrir sur la note d'un autre serait promettre un
	// 404.
	Sienne bool

	// EnEdition remplace la note par son formulaire, et Texte porte alors ce
	// que le champ propose — l'enregistré, ou la saisie qui vient d'être
	// refusée.
	EnEdition bool
	Texte     string
}

// brancheLesCommentaires pose les quatre routes des notes.
//
// Toutes derrière exigeUneSession, comme celles des recettes : le contrôle
// passe avant la recherche de la recette et avant la lecture du formulaire.
// Toutes en POST plutôt qu'en PUT ou DELETE, comme le reste du produit : un
// formulaire HTML ne sait pas émettre autre chose, et l'interface doit
// fonctionner sans JavaScript.
func brancheLesCommentaires(routeur *router.Router[*core.RequestEvent]) {
	notes := "/recettes/{id}/commentaires"
	routeur.POST(notes, laRouteDUneNote(ajouteUneNote)).Bind(exigeUneSession())
	routeur.GET(notes+"/{idc}/modifier", laRouteDUneNote(pageModifierUneNote)).Bind(exigeUneSession())
	routeur.POST(notes+"/{idc}", laRouteDUneNote(metAJourUneNote)).Bind(exigeUneSession())
	routeur.POST(notes+"/{idc}/supprimer", laRouteDUneNote(supprimeUneNote)).Bind(exigeUneSession())
}

// laRouteDUneNote traduit erreurIntrouvable en la 404 de la fiche.
//
// Un compte connecté qui vise la note d'un autre obtient donc un 404, et non un
// 403 : l'existence d'une note d'autrui n'est pas une information à donner par
// un code de statut.
func laRouteDUneNote(traite func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		err := traite(e)
		if errors.Is(err, erreurIntrouvable) {
			return pageRecetteIntrouvable(e)
		}
		return err
	}
}

// ajouteUneNote enregistre une note signée du compte de la session.
func ajouteUneNote(e *core.RequestEvent) error {
	recette, err := laRecetteCommentee(e)
	if err != nil {
		return err
	}

	saisi, corps, message := corpsSoumis(e)
	if message != "" {
		bloc, err := blocDesNotes(e, recette, "")
		if err != nil {
			return err
		}
		bloc.Message = message
		bloc.Brouillon = saisi
		return rendLeBloc(e, recette, bloc)
	}

	collection, err := e.App.FindCollectionByNameOrId("comments")
	if err != nil {
		return err
	}
	note := core.NewRecord(collection)
	note.Set("recipe", recette.Id)
	// L'auteur vient de la session, jamais du formulaire — même règle que
	// created_by sur les recettes. Les règles de collection rendent author
	// infalsifiable par l'API REST ; ce chemin-ci ne les traverse pas, et c'est
	// ici que la signature se pose.
	note.Set("author", e.Auth.Id)
	note.Set("body", corps)
	if err := e.App.Save(note); err != nil {
		return err
	}

	return repondApresEcriture(e, recette)
}

// pageModifierUneNote rend la liste avec cette note-là remplacée par son
// formulaire d'édition.
func pageModifierUneNote(e *core.RequestEvent) error {
	recette, note, err := laNoteDemandee(e)
	if err != nil {
		return err
	}

	bloc, err := blocDesNotes(e, recette, note.Id)
	if err != nil {
		return err
	}
	return rendLeBloc(e, recette, bloc)
}

// metAJourUneNote réécrit le texte, et lui seul : author, recipe et created ne
// sont pas touchés — une note qui changerait d'auteur en se corrigeant ne
// serait plus signée.
func metAJourUneNote(e *core.RequestEvent) error {
	recette, note, err := laNoteDemandee(e)
	if err != nil {
		return err
	}

	saisi, corps, message := corpsSoumis(e)
	if message != "" {
		bloc, err := blocDesNotes(e, recette, note.Id)
		if err != nil {
			return err
		}
		bloc.Message = message
		bloc.reproposeLeTexte(note.Id, saisi)
		return rendLeBloc(e, recette, bloc)
	}

	note.Set("body", corps)
	if err := e.App.Save(note); err != nil {
		return err
	}

	return repondApresEcriture(e, recette)
}

// supprimeUneNote retire la note du compte connecté.
func supprimeUneNote(e *core.RequestEvent) error {
	recette, note, err := laNoteDemandee(e)
	if err != nil {
		return err
	}

	if err := e.App.Delete(note); err != nil {
		return err
	}
	return repondApresEcriture(e, recette)
}

// --- Ce que les quatre routes ont en commun --------------------------------

// laRecetteCommentee rend la recette de l'URL, ou erreurIntrouvable.
func laRecetteCommentee(e *core.RequestEvent) (*core.Record, error) {
	recette, err := e.App.FindRecordById("recipes", e.Request.PathValue("id"))
	if err != nil {
		return nil, fmt.Errorf("recette %s : %w", e.Request.PathValue("id"), erreurIntrouvable)
	}
	return recette, nil
}

// laNoteDemandee rend la recette et la note visées, ou erreurIntrouvable.
//
// La propriété se vérifie ici, dans le code de la route, et pas seulement par
// la règle de collection : ces routes sont servies par notre code Go, que les
// règles ne gardent pas. Une note rattachée à une autre recette est traitée de
// même — l'URL doit désigner ce qu'elle prétend désigner.
func laNoteDemandee(e *core.RequestEvent) (*core.Record, *core.Record, error) {
	recette, err := laRecetteCommentee(e)
	if err != nil {
		return nil, nil, err
	}

	note, err := e.App.FindRecordById("comments", e.Request.PathValue("idc"))
	if err != nil {
		return nil, nil, fmt.Errorf("note %s : %w", e.Request.PathValue("idc"), erreurIntrouvable)
	}
	if note.GetString("recipe") != recette.Id || note.GetString("author") != e.Auth.Id {
		return nil, nil, fmt.Errorf("note %s hors de portée : %w", note.Id, erreurIntrouvable)
	}
	return recette, note, nil
}

// corpsSoumis lit le texte posté et dit ce qui cloche.
//
// Trois retours : ce que l'utilisateur a tapé — pour le lui rendre —, le texte
// à enregistrer une fois rogné, et le message en français si la saisie est
// refusée. Rien n'est écrit tant que le message n'est pas vide.
func corpsSoumis(e *core.RequestEvent) (saisi, corps, message string) {
	champs, err := valeursSoumises(e)
	if err != nil {
		return "", "", "Cette note n'a pas pu être lue."
	}

	// author n'est jamais lu ici, et c'est le seul endroit où il pourrait
	// l'être : un champ posté à ce nom n'a nulle part où atterrir.
	saisi = champs.Get("corps")
	corps = strings.TrimSpace(saisi)

	switch {
	case corps == "":
		return saisi, "", "Une note ne peut pas être vide."
	case len([]rune(corps)) > corpsMaxDUneNote:
		return saisi, "", fmt.Sprintf(
			"Une note ne peut pas dépasser %d caractères.", corpsMaxDUneNote)
	}
	return saisi, corps, ""
}

// repondApresEcriture renvoie le bloc à HTMX, ou la fiche au navigateur.
//
// Sans l'en-tête, une redirection plutôt qu'un rendu : c'est ce qui évite qu'un
// rechargement de page rejoue l'écriture. L'ajout, la modification et la
// suppression fonctionnent donc sans JavaScript, HTMX ne faisant qu'éviter le
// rechargement.
func repondApresEcriture(e *core.RequestEvent, recette *core.Record) error {
	if !estHTMX(e) {
		return e.Redirect(http.StatusSeeOther, "/recettes/"+recette.Id)
	}

	bloc, err := blocDesNotes(e, recette, "")
	if err != nil {
		return err
	}
	return rendLeBloc(e, recette, bloc)
}

// rendLeBloc écrit le bloc seul à HTMX, ou la fiche entière au navigateur.
//
// La fiche n'est reconstruite que dans le second cas : une réponse à HTMX n'en
// affiche rien, et la bâtir coûterait deux requêtes pour du HTML jeté.
func rendLeBloc(e *core.RequestEvent, recette *core.Record, bloc *donneesCommentaires) error {
	donnees := &donneesPage{Commentaires: bloc}

	if !estHTMX(e) {
		fiche, err := ficheDeLaRecette(e.App, recette)
		if err != nil {
			return err
		}
		donnees.Titre = recette.GetString("title") + " — Patachoo"
		donnees.Recette = fiche
	}

	return rendre(e, "recette.html", "commentaires.html", donnees, "recette-corps.html")
}

// --- Le bloc ---------------------------------------------------------------

// blocDesNotes lit les notes d'une recette et les met en forme.
//
// enEdition porte l'identifiant de la note à remplacer par son formulaire, ou
// "" quand la liste se rend telle quelle.
func blocDesNotes(e *core.RequestEvent, recette *core.Record, enEdition string) (*donneesCommentaires, error) {
	// Décroissant sur created : le plus récent en premier, et c'est l'ordre que
	// l'index idx_comments_recipe_created porte déjà.
	notes, err := e.App.FindRecordsByFilter(
		"comments",
		"recipe = {:recette}",
		"-created",
		0,
		0,
		dbx.Params{"recette": recette.Id},
	)
	if err != nil {
		return nil, fmt.Errorf("notes de %s : %w", recette.Id, err)
	}

	bloc := &donneesCommentaires{RecetteId: recette.Id}
	if len(notes) == 0 {
		return bloc, nil
	}

	// L'expansion résout le nom de l'auteur par notre rendu serveur : users se
	// ferme sur elle-même par ses règles, donc un expand côté client ne
	// rendrait rien. C'est aussi pourquoi seul name en ressort.
	if echecs := e.App.ExpandRecords(notes, []string{"author"}, nil); len(echecs) > 0 {
		return nil, fmt.Errorf("auteurs des notes de %s : %v", recette.Id, echecs)
	}

	bloc.Notes = make([]noteAffichee, 0, len(notes))
	for _, note := range notes {
		affichee := noteAffichee{
			Id:          note.Id,
			Auteur:      nomDeLAuteur(note.ExpandedOne("author")),
			Date:        dateEnFrancais(note.GetDateTime("created")),
			Modifiee:    dateDeReecriture(note),
			Paragraphes: paragraphes(note.GetString("body")),
			Sienne:      note.GetString("author") == e.Auth.Id,
			EnEdition:   note.Id == enEdition,
			Texte:       note.GetString("body"),
		}
		bloc.Notes = append(bloc.Notes, affichee)
	}
	return bloc, nil
}

// reproposeLeTexte remet dans le formulaire d'édition ce que l'utilisateur
// venait de taper : sans ça, un refus lui rendrait le texte enregistré, et il
// retaperait sa correction.
func (d *donneesCommentaires) reproposeLeTexte(id, texte string) {
	for i := range d.Notes {
		if d.Notes[i].Id == id {
			d.Notes[i].Texte = texte
		}
	}
}

// nomDeLAuteur rend users.name, ou le repli. Le nom, et rien d'autre.
func nomDeLAuteur(auteur *core.Record) string {
	if auteur == nil {
		return nomDeReplietDeLAuteur
	}
	if nom := strings.TrimSpace(auteur.GetString("name")); nom != "" {
		return nom
	}
	return nomDeReplietDeLAuteur
}

// dateDeReecriture rend la date de la dernière réécriture, ou "" si la note
// n'a jamais bougé.
func dateDeReecriture(note *core.Record) string {
	cree := note.GetDateTime("created").Time()
	miseAJour := note.GetDateTime("updated").Time()
	if miseAJour.Sub(cree) < ecartDeReecriture {
		return ""
	}
	return dateEnFrancais(note.GetDateTime("updated"))
}

// paragraphes découpe le texte, une entrée par ligne non vide — exactement le
// traitement des instructions, tranché le 19/08/2026. Les lignes vides séparent
// sans produire de paragraphe vide.
func paragraphes(corps string) []string {
	var decoupes []string
	for _, ligne := range strings.Split(corps, "\n") {
		if ligne := strings.TrimSpace(ligne); ligne != "" {
			decoupes = append(decoupes, ligne)
		}
	}
	return decoupes
}

// mois porte les douze noms français : Go ne sait pas les écrire, et une
// dépendance de localisation pour douze mots serait chère payée.
var mois = [...]string{
	"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre",
}

// dateEnFrancais rend « 19 août 2026 ».
//
// Le jour, le mois et l'année, sans heure : une note de cuisine se situe dans
// la semaine, pas dans la minute. La date est lue telle que PocketBase la
// stocke, en temps universel — la traduire dans le fuseau du serveur ferait
// dépendre le rendu d'un réglage de machine.
func dateEnFrancais(quand types.DateTime) string {
	if quand.IsZero() {
		return ""
	}
	date := quand.Time()
	return fmt.Sprintf("%d %s %d", date.Day(), mois[date.Month()-1], date.Year())
}
