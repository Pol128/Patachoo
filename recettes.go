package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	validation "github.com/pocketbase/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// --- La liste et la recherche ----------------------------------------------

// parPage : le nombre de vignettes d'une page. Pagination classique et non
// défilement progressif — une URL partageable, une page imprimable, et des
// critères qui tiennent tous dans la chaîne de requête.
const parPage = 24

// pageMax borne le numéro de page accepté.
//
// Ce n'est pas un confort d'affichage : le décalage se calcule par
// (page-1)*parPage, et un « ?page=999999999999999999 » le ferait déborder en
// négatif — que le décalage de la requête ignore, rendant alors la première
// page à qui a demandé la dernière. Une borne bien au-delà de tout carnet réel.
const pageMax = 1 << 20

// criteres porte ce que la chaîne de requête dit de la liste.
//
// Un seul type et un seul lecteur : les filtres par tag, par type de plat et
// par saison (PATA-16, PATA-17, PATA-18) s'y ajouteront sans que la liste
// n'ait à être réécrite.
type criteres struct {
	Terme string
	Tag   string
	Page  int
}

// lisLesCriteres est le seul endroit où la chaîne de requête est lue.
//
// Un paramètre malmené ne produit pas d'erreur : une page non numérique, nulle
// ou négative retombe sur la première. Refuser vaudrait une 500 pour un lien
// mal recopié.
func lisLesCriteres(r *http.Request) criteres {
	requete := r.URL.Query()

	page, err := strconv.Atoi(requete.Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	if page > pageMax {
		page = pageMax
	}

	return criteres{
		Terme: strings.TrimSpace(requete.Get("q")),
		Tag:   strings.TrimSpace(requete.Get("tag")),
		Page:  page,
	}
}

// sansTag rend les mêmes critères, le tag ôté et la pagination remise au
// début : la page 3 d'une liste filtrée ne désigne pas la même chose une fois
// le filtre retiré, et un lien de sortie doit ramener quelque part.
func (c criteres) sansTag() criteres {
	c.Tag = ""
	c.Page = 1
	return c
}

// lien rend l'URL de la liste pour ces critères, page comprise.
//
// La page 1 et un terme vide ne s'écrivent pas : « /recettes » et
// « /recettes?page=1 » désigneraient la même chose sous deux adresses.
func (c criteres) lien(page int) string {
	valeurs := url.Values{}
	if c.Terme != "" {
		valeurs.Set("q", c.Terme)
	}
	if c.Tag != "" {
		valeurs.Set("tag", c.Tag)
	}
	if page > 1 {
		valeurs.Set("page", strconv.Itoa(page))
	}

	if len(valeurs) == 0 {
		return "/recettes"
	}
	return "/recettes?" + valeurs.Encode()
}

// vignette : ce qu'une case de la grille affiche, et rien de plus.
//
// Les champs facultatifs sont vides quand la donnée manque, et le gabarit
// n'écrit alors rien : ni cadre vide, ni libellé orphelin.
type vignette struct {
	Id         string
	Titre      string
	Miniature  string
	TypeDePlat string
	Tags       []lienDeTag
}

// donneesRecettes est ce que la page de liste donne à ses gabarits.
type donneesRecettes struct {
	donneesPage
	Terme     string
	Vignettes []vignette

	// Tag est le slug filtré, NomDuTag ce que la page en affiche, et SansTag
	// l'adresse de la même liste sans lui. Les trois sont vides ensemble :
	// sans filtre, il n'y a ni bandeau à écrire ni sortie à proposer.
	Tag      string
	NomDuTag string
	SansTag  string

	CarnetVide bool
	Precedente string
	Suivante   string
}

// pageListeRecettes rend la liste des recettes : la page d'accueil de l'outil,
// et la cible des liens de toutes les autres pages.
//
// La route vérifie la session elle-même : servie par notre code Go, elle est
// hors des règles de collection, qui ne gardent que l'API REST. Sans session,
// le carnet n'est pas rendu et la requête part vers la page de connexion.
func pageListeRecettes(e *core.RequestEvent) error {
	if e.Auth == nil {
		return e.Redirect(http.StatusFound, "/connexion")
	}

	criteres := lisLesCriteres(e.Request)

	// Une vignette de plus que la page : c'est ce dépassement, et lui seul, qui
	// dit qu'il existe un rang suivant. La requête FTS5 saurait maintenant se
	// compter, mais un total n'est pas ce qu'on demande ici — « y en a-t-il
	// d'autres » coûte une ligne lue en trop, une seconde requête coûterait
	// plus.
	trouvees, err := recettesDuRang(e.App, criteres)
	if err != nil {
		return err
	}

	suivante := len(trouvees) > parPage
	if suivante {
		trouvees = trouvees[:parPage]
	}

	vignettes, err := vignettesDe(e.App, trouvees)
	if err != nil {
		return err
	}

	donnees := donneesRecettes{
		donneesPage: donneesPage{Titre: "Recettes — Patachoo"},
		Terme:       criteres.Terme,
		Vignettes:   vignettes,
		// Le carnet vide se distingue de la recherche sans résultat : les
		// confondre afficherait « votre carnet est vide » à quelqu'un qui a
		// simplement mal orthographié un mot. Il se déduit sans compter :
		// la première page, sans terme, ne peut être vide que si le carnet
		// l'est.
		CarnetVide: len(vignettes) == 0 && criteres.Terme == "" && criteres.Tag == "" && criteres.Page == 1,
	}
	if criteres.Tag != "" {
		donnees.Tag = criteres.Tag
		donnees.NomDuTag = nomDuTag(e.App, criteres.Tag)
		donnees.SansTag = criteres.sansTag().lien(1)
	}
	if criteres.Page > 1 {
		donnees.Precedente = criteres.lien(criteres.Page - 1)
	}
	if suivante {
		donnees.Suivante = criteres.lien(criteres.Page + 1)
	}

	return rendre(e, "recettes.html", "recettes-resultats.html", &donnees)
}

// recettesDuRang lit une page de recettes, une de plus que nécessaire.
//
// Une requête construite, et non plus FindRecordsByFilter : MATCH ne s'exprime
// pas dans le filtre en chaîne de PocketBase. Il n'y a pour autant aucune
// jointure à écrire sur les ingrédients ni sur les tags — l'index les porte
// déjà, une ligne par recette, et rien n'est donc à dédupliquer.
//
// RecordQuery rend des *core.Record comme FindRecordsByFilter : le rendu de la
// grille ne change pas.
func recettesDuRang(app core.App, criteres criteres) ([]*core.Record, error) {
	requete := app.RecordQuery("recipes")
	if criteres.Tag != "" {
		// « Au moins un tag correspond », et non « tous » : une recette à deux
		// tags dont un seul est demandé doit rester, sinon un clic sur un tag
		// ferait disparaître les recettes les mieux étiquetées.
		//
		// recipes.tags est une colonne JSON DEFAULT '[]' NOT NULL, comme les
		// déclencheurs de l'index de recherche l'exploitent déjà : json_each
		// s'y applique sans précaution. Le slug demandé passe par les
		// paramètres, jamais par concaténation.
		requete = requete.AndWhere(dbx.NewExp(
			`EXISTS (SELECT 1 FROM json_each(recipes.tags) tag_lie
			         JOIN tags ON tags.id = tag_lie.value
			         WHERE tags.slug = {:tag})`,
			dbx.Params{"tag": criteres.Tag}))
	}
	if motif := motifDeRecherche(criteres.Terme); motif != "" {
		requete = requete.
			InnerJoin("recipes_fts", dbx.NewExp("recipes_fts.recipe_id = recipes.id")).
			AndWhere(dbx.NewExp("recipes_fts MATCH {:q}", dbx.Params{"q": motif}))
	}

	recettes := []*core.Record{}
	err := requete.
		OrderBy("recipes.created DESC").
		Limit(parPage + 1).
		Offset(int64((criteres.Page - 1) * parPage)).
		All(&recettes)
	return recettes, err
}

// motifDeRecherche traduit le terme saisi en requête FTS5, ou rend "" si le
// terme ne porte aucun mot — auquel cas la liste n'a pas de critère et rend le
// carnet entier. Une chaîne MATCH vide serait une erreur de syntaxe FTS5 :
// elle n'est jamais exécutée.
//
// La syntaxe de requête de FTS5 est un langage. Un terme portant un guillemet,
// « * », « NEAR », « AND », « OR » ou « - » change le sens de la requête, voire
// la fait échouer. Chaque mot part donc entre guillemets doubles — guillemet
// interne doublé — et suffixé de « * », qui est la recherche par préfixe. C'est
// le pendant de l'échappement de « % » et « _ » que le LIKE demandait.
//
// Citer n'est pas lier : le motif rejoint le SQL par dbx.Params, jamais par
// concaténation. La citation répond à la syntaxe de la requête, le paramètre
// lié à l'injection ; les deux sont nécessaires, aucune ne dispense de l'autre.
func motifDeRecherche(terme string) string {
	mots := strings.Fields(terme)
	if len(mots) == 0 {
		return ""
	}

	cites := make([]string, 0, len(mots))
	for _, mot := range mots {
		cites = append(cites, `"`+strings.ReplaceAll(mot, `"`, `""`)+`"*`)
	}
	// Les mots juxtaposés sont un ET implicite, insensible à leur ordre.
	return strings.Join(cites, " ")
}

// vignettesDe traduit les enregistrements en ce que la grille affiche.
func vignettesDe(app core.App, recettes []*core.Record) ([]vignette, error) {
	if len(recettes) == 0 {
		return nil, nil
	}

	if echecs := app.ExpandRecords(recettes, []string{"tags", "meal_type"}, nil); len(echecs) > 0 {
		return nil, fmt.Errorf("relations des recettes : %v", echecs)
	}

	vignettes := make([]vignette, 0, len(recettes))
	for _, recette := range recettes {
		vignettes = append(vignettes, vignette{
			Id:         recette.Id,
			Titre:      recette.GetString("title"),
			Miniature:  miniature(recette),
			TypeDePlat: nomDe(recette.ExpandedOne("meal_type")),
			Tags:       liensDesTags(recette.ExpandedAll("tags")),
		})
	}
	return vignettes, nil
}

// miniature rend l'URL de la vignette 300x200, ou "" si aucune image n'est
// stockée — auquel cas le gabarit n'écrit pas d'image du tout, plutôt qu'une
// image cassée.
func miniature(recette *core.Record) string {
	fichier := recette.GetString("image")
	if fichier == "" {
		return ""
	}
	return "/api/files/recipes/" + recette.Id + "/" + url.PathEscape(fichier) + "?thumb=300x200"
}

func nomDe(enregistrement *core.Record) string {
	if enregistrement == nil {
		return ""
	}
	return enregistrement.GetString("name")
}

// --- Le formulaire de création et d'édition --------------------------------

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

// optionTypeDePlat est ce que le gabarit connaît d'une entrée de meal_types : de
// quoi l'afficher et la poster, rien de plus.
type optionTypeDePlat struct {
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

	TypesDePlat      []optionTypeDePlat
	ToutesLesSaisons []string
}

// SaisieDesTags rend le champ de tags tel que son gabarit l'attend. Le
// formulaire ne propose rien à son ouverture : les suggestions arrivent à la
// frappe, par la route qui les cherche.
func (f formulaireRecette) SaisieDesTags() saisieDesTags {
	return saisieDesTags{Valeur: f.Tags}
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
			if message := refusDeLaLigne(i+1, err); message != "" {
				return erreurDeSaisie{message: message}
			}
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
		saisie.TypesDePlat = append(saisie.TypesDePlat, optionTypeDePlat{
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

	identifiants := recette.GetStringSlice("tags")
	tags, err := app.FindRecordsByIds("tags", identifiants)
	if err != nil {
		return saisie, fmt.Errorf("lecture des tags : %w", err)
	}
	// FindRecordsByIds ne rend pas les enregistrements dans l'ordre demandé :
	// le champ doit suivre l'ordre de la recette, sinon une édition sans
	// changement réécrirait les tags dans un autre ordre que celui saisi.
	parIdentifiant := make(map[string]*core.Record, len(tags))
	for _, tag := range tags {
		parIdentifiant[tag.Id] = tag
	}
	noms := make([]string, 0, len(identifiants))
	for _, identifiant := range identifiants {
		if tag := parIdentifiant[identifiant]; tag != nil {
			noms = append(noms, tag.GetString("name"))
		}
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
	// Un pointeur : rendre pose l'utilisateur courant sur la structure, et une
	// copie recevrait l'en-tête que le gabarit ne lirait pas.
	return rendre(e, "recette-formulaire.html", "recette-formulaire-corps.html", &donneesPage{
		Titre:      saisie.Legende + " — Patachoo",
		Message:    message,
		Formulaire: &saisie,
	}, "tags-saisie.html")
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

// refusDeLaLigne nomme la ligne d'ingrédient que le schéma a refusée, ou rend
// "" si le refus ne porte pas sur ce que l'utilisateur a tapé.
//
// raw est le seul champ de la collection qui vienne du formulaire ; recipe et
// position sont posés ici même, et leur refus serait une panne — la remonter
// en faute de frappe la rendrait invisible.
func refusDeLaLigne(rang int, err error) string {
	var refus validation.Errors
	if !errors.As(err, &refus) {
		return ""
	}
	if _, refuse := refus["raw"]; !refuse {
		return ""
	}
	return fmt.Sprintf(
		"Le champ « %s » n'a pas été accepté : la ligne n° %d est trop longue.",
		libelleDuChamp["raw"], rang,
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
	"raw":          "ingrédients",
}
