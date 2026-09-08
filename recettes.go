package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	validation "github.com/pocketbase/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// memoireMaxFormulaire borne ce qu'un envoi multipart garde en mémoire ; le
// reste part dans un fichier temporaire. Ce n'est pas la limite de taille du
// téléversement — celle-là est sur le schéma, et c'est lui qui la fait
// respecter.
const memoireMaxFormulaire = 8 << 20

// erreurDeSaisie est une erreur que l'utilisateur peut corriger lui-même :
// elle se rend dans le formulaire, jamais en 500.
//
// Un type à nous plutôt qu'un test sur le texte du message : c'est ce qui
// permet de la distinguer d'une panne de base au retour d'une transaction, où
// les deux arrivent par le même chemin.
type erreurDeSaisie struct{ message string }

func (e erreurDeSaisie) Error() string { return e.message }

// typeDePlat est ce que le gabarit connaît d'une entrée de meal_types : de
// quoi l'afficher et la poster, rien de plus.
type typeDePlat struct {
	Id  string
	Nom string
}

// formulaireRecette porte la saisie et ce qu'il faut pour la re-rendre.
//
// Les nombres y sont des chaînes, et c'est délibéré : après une erreur, le
// formulaire doit rendre ce que l'utilisateur a tapé, pas la valeur qu'un
// entier aurait ravalée. C'est PocketBase qui convertit à l'enregistrement.
type formulaireRecette struct {
	Legende string
	Action  string

	Titre            string
	Portions         string
	TempsPreparation string
	TempsCuisson     string
	Instructions     string
	Ingredients      string
	Tags             string
	TypeDePlat       string
	Saisons          []string

	// Image est le nom du fichier déjà enregistré : il n'est pas reposté, il
	// dit seulement s'il y a quelque chose à retirer.
	Image string

	TypesDePlat      []typeDePlat
	ToutesLesSaisons []string
}

// EstLeTypeDePlat et SaisonCochee servent au gabarit, qui ne sait pas
// chercher dans une liste.
func (f formulaireRecette) EstLeTypeDePlat(id string) bool { return f.TypeDePlat == id }

func (f formulaireRecette) SaisonCochee(saison string) bool {
	for _, cochee := range f.Saisons {
		if cochee == saison {
			return true
		}
	}
	return false
}

// lignesDIngredients rend une ligne par ingrédient, rognée, les vides ôtées.
//
// Les vides ôtées ici et pas à l'enregistrement : c'est ce qui garantit que
// les position se suivent sans trou, une ligne blanche ne consommant aucun
// rang.
func (f formulaireRecette) lignesDIngredients() []string {
	lignes := []string{}
	for _, ligne := range strings.Split(f.Ingredients, "\n") {
		if rognee := strings.TrimSpace(ligne); rognee != "" {
			lignes = append(lignes, rognee)
		}
	}
	return lignes
}

// brancheLesRecettes pose les quatre routes du formulaire.
//
// Toutes derrière exigeUneSession : ce sont des routes d'écriture ou d'accès à
// des recettes, et le contrôle passe avant tout le reste — avant la recherche
// de la recette, avant la lecture du formulaire.
func brancheLesRecettes(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/recettes/nouvelle", pageNouvelleRecette).Bind(exigeUneSession())
	routeur.POST("/recettes", creeLaRecette).Bind(exigeUneSession())
	routeur.GET("/recettes/{id}/modifier", pageModifierRecette).Bind(exigeUneSession())
	routeur.POST("/recettes/{id}", metAJourLaRecette).Bind(exigeUneSession())
}

// exigeUneSession renvoie un visiteur à la page de connexion.
//
// Une redirection et non le 401 de apis.RequireAuth : ces routes rendent des
// pages, et un navigateur à qui on répond en JSON n'affiche rien d'utile.
func exigeUneSession() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id: "patachooExigeUneSession",
		Func: func(e *core.RequestEvent) error {
			if e.Auth == nil {
				return e.Redirect(http.StatusSeeOther, "/connexion")
			}
			return e.Next()
		},
	}
}

// pageNouvelleRecette rend le formulaire vide.
func pageNouvelleRecette(e *core.RequestEvent) error {
	saisie, err := formulaireVide(e.App)
	if err != nil {
		return err
	}
	saisie.Legende = "Nouvelle recette"
	saisie.Action = "/recettes"

	return rendLeFormulaire(e, saisie, "")
}

// pageModifierRecette rend le formulaire pré-rempli.
func pageModifierRecette(e *core.RequestEvent) error {
	recette, err := laRecetteDemandee(e)
	if err != nil {
		return err
	}

	saisie, err := formulaireDepuis(e.App, recette)
	if err != nil {
		return err
	}

	return rendLeFormulaire(e, saisie, "")
}

// creeLaRecette enregistre une recette neuve.
func creeLaRecette(e *core.RequestEvent) error {
	collection, err := e.App.FindCollectionByNameOrId("recipes")
	if err != nil {
		return err
	}

	recette := core.NewRecord(collection)
	// L'auteur vient de la session, jamais du formulaire : un champ posté
	// serait falsifiable. Les hooks de requête ne couvrent pas ce chemin —
	// ils ne se déclenchent que pour l'API REST.
	poseLAuteur(recette, e.Auth)

	saisie, err := formulaireSoumis(e)
	if err != nil {
		return err
	}
	saisie.Legende = "Nouvelle recette"
	saisie.Action = "/recettes"

	return enregistre(e, recette, saisie)
}

// metAJourLaRecette réécrit une recette existante.
//
// source_url, source_name et created_by ne sont pas touchés : corriger une
// recette importée ne doit pas lui faire perdre son origine ni son auteur.
func metAJourLaRecette(e *core.RequestEvent) error {
	recette, err := laRecetteDemandee(e)
	if err != nil {
		return err
	}

	saisie, err := formulaireSoumis(e)
	if err != nil {
		return err
	}
	saisie.Legende = "Modifier la recette"
	saisie.Action = "/recettes/" + recette.Id
	saisie.Image = recette.GetString("image")

	return enregistre(e, recette, saisie)
}

// laRecetteDemandee lit l'identifiant de l'URL et rend la recette.
func laRecetteDemandee(e *core.RequestEvent) (*core.Record, error) {
	recette, err := e.App.FindRecordById("recipes", e.Request.PathValue("id"))
	if err != nil {
		return nil, e.NotFoundError("Cette recette n'existe pas.", err)
	}
	return recette, nil
}

// enregistre écrit la recette et ses ingrédients, ou re-rend le formulaire.
//
// Tout dans une transaction : une recette enregistrée dont les ingrédients
// échouent laisserait une fiche amputée que personne ne saurait rattraper.
func enregistre(e *core.RequestEvent, recette *core.Record, saisie formulaireRecette) error {
	if strings.TrimSpace(saisie.Titre) == "" {
		return rendLeFormulaire(e, saisie, "Le titre est obligatoire.")
	}

	err := e.App.RunInTransaction(func(txApp core.App) error {
		if err := poseLesChamps(txApp, e, recette, saisie); err != nil {
			return err
		}
		if err := txApp.Save(recette); err != nil {
			return err
		}
		return remplaceLesIngredients(txApp, recette, saisie.lignesDIngredients())
	})
	if err != nil {
		if message := messageDeSaisie(recette.Collection(), err); message != "" {
			return rendLeFormulaire(e, saisie, message)
		}
		return err
	}

	// La fiche est PATA-14 : elle n'existe pas encore, mais c'est déjà son
	// URL — une page intermédiaire serait à défaire ensuite.
	return e.Redirect(http.StatusSeeOther, "/recettes/"+recette.Id)
}

// poseLesChamps recopie la saisie sur l'enregistrement.
//
// Les nombres partent en chaînes : PocketBase les convertit à la validation,
// et c'est lui qui décide ce qu'un champ accepte.
func poseLesChamps(txApp core.App, e *core.RequestEvent, recette *core.Record, saisie formulaireRecette) error {
	recette.Set("title", strings.TrimSpace(saisie.Titre))
	recette.Set("servings", saisie.Portions)
	recette.Set("prep_time", saisie.TempsPreparation)
	recette.Set("cook_time", saisie.TempsCuisson)
	// Texte brut, stocké tel quel : le champ est un EditorField au schéma,
	// mais rien n'y verse de HTML et les gabarits l'échappent partout.
	recette.Set("instructions", saisie.Instructions)
	recette.Set("meal_type", saisie.TypeDePlat)
	recette.Set("seasons", saisie.Saisons)

	tags, err := tagsDepuisSaisie(txApp, saisie.Tags)
	if err != nil {
		// Trop de tags distincts est une faute de saisie, pas une panne.
		return erreurDeSaisie{message: err.Error()}
	}
	ids := make([]string, 0, len(tags))
	for _, tag := range tags {
		ids = append(ids, tag.Id)
	}
	recette.Set("tags", ids)

	return poseLImage(e, recette)
}

// poseLImage applique le téléversement, l'effacement, ou ne touche à rien.
//
// Ne rien faire est le cas ordinaire d'une édition : un formulaire renvoyé
// sans fichier ne doit pas effacer l'image en place.
func poseLImage(e *core.RequestEvent, recette *core.Record) error {
	if e.Request.PostFormValue("retirer-image") != "" {
		recette.Set("image", nil)
		return nil
	}

	fichier, entete, err := e.Request.FormFile("image")
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) || errors.Is(err, http.ErrNotMultipart) {
			return nil
		}
		return err
	}
	defer fichier.Close()

	televerse, err := filesystem.NewFileFromMultipart(entete)
	if err != nil {
		return err
	}
	recette.Set("image", televerse)
	return nil
}

// remplaceLesIngredients réécrit la liste en bloc.
//
// Supprimer puis recréer, et non rapprocher ligne à ligne : c'est la règle la
// plus simple qui garantisse que ce qui est affiché est ce qui est enregistré.
// Sa conséquence — les identifiants changent à chaque édition — est assumée
// tant que rien ne s'y accroche.
func remplaceLesIngredients(txApp core.App, recette *core.Record, lignes []string) error {
	anciennes, err := txApp.FindAllRecords("ingredients", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		return fmt.Errorf("lecture des ingrédients : %w", err)
	}
	for _, ancienne := range anciennes {
		if err := txApp.Delete(ancienne); err != nil {
			return fmt.Errorf("suppression de l'ingrédient %s : %w", ancienne.Id, err)
		}
	}

	collection, err := txApp.FindCollectionByNameOrId("ingredients")
	if err != nil {
		return fmt.Errorf("collection ingredients : %w", err)
	}
	for i, brut := range lignes {
		ligne := core.NewRecord(collection)
		ligne.Set("recipe", recette.Id)
		// Les rangs commencent à 1 : à 0, le premier ingrédient ne se
		// distinguerait pas d'un rang jamais renseigné.
		ligne.Set("position", i+1)
		// raw seul : les champs du parser sont alimentés par le hook de
		// lecture des lignes, qui couvre tous les chemins d'écriture.
		ligne.Set("raw", brut)
		if err := txApp.Save(ligne); err != nil {
			return fmt.Errorf("enregistrement de l'ingrédient %q : %w", brut, err)
		}
	}
	return nil
}

// --- La saisie -------------------------------------------------------------

// formulaireSoumis lit ce que la requête porte.
func formulaireSoumis(e *core.RequestEvent) (formulaireRecette, error) {
	saisie, err := formulaireVide(e.App)
	if err != nil {
		return saisie, err
	}

	champs, err := valeursSoumises(e)
	if err != nil {
		return saisie, err
	}

	saisie.Titre = champs.Get("titre")
	saisie.Portions = champs.Get("portions")
	saisie.TempsPreparation = champs.Get("temps-preparation")
	saisie.TempsCuisson = champs.Get("temps-cuisson")
	saisie.Instructions = champs.Get("instructions")
	saisie.Ingredients = champs.Get("ingredients")
	saisie.Tags = champs.Get("tags")
	saisie.TypeDePlat = champs.Get("type-de-plat")
	saisie.Saisons = champs["saisons"]

	// created_by n'est jamais lu ici, et c'est le seul endroit où il pourrait
	// l'être : un champ posté à ce nom n'a nulle part où atterrir.
	return saisie, nil
}

// valeursSoumises rend les champs du corps, multipart ou non.
func valeursSoumises(e *core.RequestEvent) (url.Values, error) {
	err := e.Request.ParseMultipartForm(memoireMaxFormulaire)
	if err != nil && !errors.Is(err, http.ErrNotMultipart) {
		return nil, err
	}
	return e.Request.PostForm, nil
}

// formulaireVide rend un formulaire sans saisie, mais garni de ce que le
// schéma propose : les types de plat et les saisons.
func formulaireVide(app core.App) (formulaireRecette, error) {
	saisie := formulaireRecette{Saisons: []string{}}

	types, err := app.FindAllRecords("meal_types")
	if err != nil {
		return saisie, fmt.Errorf("lecture des types de plat : %w", err)
	}
	// L'ordre vient de position, comme la barre de filtres : un tri
	// alphabétique mettrait « Dessert » avant « Entrée ».
	trieParPosition(types)
	for _, type_ := range types {
		saisie.TypesDePlat = append(saisie.TypesDePlat, typeDePlat{
			Id:  type_.Id,
			Nom: type_.GetString("name"),
		})
	}

	recettes, err := app.FindCollectionByNameOrId("recipes")
	if err != nil {
		return saisie, fmt.Errorf("collection recipes : %w", err)
	}
	// Les quatre valeurs sont lues sur le schéma, jamais recopiées : une
	// liste en double finirait par diverger de celle qui valide.
	if champ, ok := recettes.Fields.GetByName("seasons").(*core.SelectField); ok {
		saisie.ToutesLesSaisons = champ.Values
	}

	return saisie, nil
}

// formulaireDepuis remplit le formulaire avec ce qui est enregistré.
func formulaireDepuis(app core.App, recette *core.Record) (formulaireRecette, error) {
	saisie, err := formulaireVide(app)
	if err != nil {
		return saisie, err
	}

	saisie.Legende = "Modifier la recette"
	saisie.Action = "/recettes/" + recette.Id
	saisie.Titre = recette.GetString("title")
	saisie.Portions = nombreAffiche(recette, "servings")
	saisie.TempsPreparation = nombreAffiche(recette, "prep_time")
	saisie.TempsCuisson = nombreAffiche(recette, "cook_time")
	saisie.Instructions = recette.GetString("instructions")
	saisie.TypeDePlat = recette.GetString("meal_type")
	saisie.Saisons = recette.GetStringSlice("seasons")
	saisie.Image = recette.GetString("image")

	lignes, err := app.FindAllRecords("ingredients", dbx.HashExp{"recipe": recette.Id})
	if err != nil {
		return saisie, fmt.Errorf("lecture des ingrédients : %w", err)
	}
	trieParPosition(lignes)
	brutes := make([]string, 0, len(lignes))
	for _, ligne := range lignes {
		brutes = append(brutes, ligne.GetString("raw"))
	}
	saisie.Ingredients = strings.Join(brutes, "\n")

	tags, err := app.FindRecordsByIds("tags", recette.GetStringSlice("tags"))
	if err != nil {
		return saisie, fmt.Errorf("lecture des tags : %w", err)
	}
	noms := make([]string, 0, len(tags))
	for _, tag := range tags {
		noms = append(noms, tag.GetString("name"))
	}
	saisie.Tags = strings.Join(noms, ", ")

	return saisie, nil
}

// nombreAffiche rend le nombre tel qu'il se retape, et la chaîne vide pour un
// champ jamais renseigné : « 0 portion » est une information, un champ vide en
// est une autre.
func nombreAffiche(recette *core.Record, champ string) string {
	if recette.GetInt(champ) == 0 {
		return ""
	}
	return fmt.Sprint(recette.GetInt(champ))
}

// trieParPosition ordonne les enregistrements sur leur champ position.
func trieParPosition(enregistrements []*core.Record) {
	slices.SortStableFunc(enregistrements, func(a, b *core.Record) int {
		return a.GetInt("position") - b.GetInt("position")
	})
}

// --- Le rendu et les erreurs ----------------------------------------------

// rendLeFormulaire rend le formulaire, avec un message s'il y en a un.
func rendLeFormulaire(e *core.RequestEvent, saisie formulaireRecette, message string) error {
	return rendre(e, "recette-formulaire.html", "recette-formulaire-corps.html", donneesPage{
		Titre:      saisie.Legende + " — Patachoo",
		Message:    message,
		Formulaire: &saisie,
	})
}

// messageDeSaisie traduit une erreur d'enregistrement en message affichable,
// ou rend "" si l'erreur n'est pas imputable à la saisie.
//
// Le tri est ce qui sépare un formulaire mal rempli — que l'utilisateur peut
// corriger — d'une panne, qui doit remonter en 500 plutôt que de se déguiser
// en faute de frappe.
func messageDeSaisie(collection *core.Collection, err error) string {
	var deSaisie erreurDeSaisie
	if errors.As(err, &deSaisie) {
		return deSaisie.Error()
	}

	var refus validation.Errors
	if !errors.As(err, &refus) {
		return ""
	}

	messages := make([]string, 0, len(refus))
	for _, champ := range collection.Fields {
		if _, refuse := refus[champ.GetName()]; !refuse {
			continue
		}
		messages = append(messages, refusDuChamp(champ))
	}
	if len(messages) == 0 {
		return ""
	}
	return strings.Join(messages, " ")
}

// refusDuChamp nomme le champ fautif, dans les mots du formulaire.
//
// Les bornes du fichier sont lues sur le schéma et jamais recopiées : un
// message qui répète « 5 Mio » ment le jour où le schéma change.
func refusDuChamp(champ core.Field) string {
	libelle := libelleDuChamp[champ.GetName()]
	if libelle == "" {
		libelle = champ.GetName()
	}

	fichier, estUnFichier := champ.(*core.FileField)
	if !estUnFichier {
		return fmt.Sprintf("Le champ « %s » n'a pas été accepté.", libelle)
	}

	formats := make([]string, 0, len(fichier.MimeTypes))
	for _, mime := range fichier.MimeTypes {
		formats = append(formats, strings.ToUpper(strings.TrimPrefix(mime, "image/")))
	}
	return fmt.Sprintf(
		"Le champ « %s » n'a pas été accepté : %d Mio au plus, au format %s.",
		libelle, fichier.MaxSize>>20, strings.Join(formats, ", "),
	)
}

// libelleDuChamp traduit les noms du schéma en noms du formulaire : c'est le
// champ que l'utilisateur voit qu'il faut lui désigner.
var libelleDuChamp = map[string]string{
	"title":        "titre",
	"image":        "image",
	"servings":     "portions",
	"prep_time":    "temps de préparation",
	"cook_time":    "temps de cuisson",
	"instructions": "instructions",
	"meal_type":    "type de plat",
	"seasons":      "saisons",
	"tags":         "tags",
}
