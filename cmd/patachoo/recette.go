package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// miniatureDeLaFiche : la fiche affiche la vignette déclarée par le schéma, pas
// le fichier d'origine. Une photo de recette pèse volontiers deux mégaoctets, et
// c'est la page qu'on ouvre les mains dans la farine.
const miniatureDeLaFiche = "800x0"

// portionsMaximum borne le choix des portions sur la fiche (PATA-138). Au-delà,
// le paramètre est ignoré comme toute valeur qu'on ne sait pas lire : la fiche
// d'un gâteau pour mille n'est pas une page qu'on cuisine.
const portionsMaximum = 100

// donneesRecette porte la fiche telle que le gabarit la lit.
//
// Tout y est déjà mis en forme — durées, quantités, listes de noms — parce que
// le gabarit n'a pas de quoi le faire : il affiche ou il n'affiche pas. Un
// champ vide y signifie donc « ce bloc n'existe pas », et c'est ce qui fait
// disparaître les libellés des données manquantes.
type donneesRecette struct {
	// Id est ce qui rend la fiche capable de se désigner elle-même : le lien
	// vers son formulaire d'édition en a besoin, et le gabarit n'a pas
	// l'enregistrement sous la main.
	Id string

	// Sienne dit que la recette est celle du compte connecté, et commande le
	// lien de suppression : l'offrir sur la recette d'un autre serait
	// promettre un 404. Le nom est celui que noteAffichee emploie déjà pour le
	// même rôle (commentaires.go).
	Sienne bool

	// Portions est le nombre de portions affiché, celui de la recette ou celui
	// demandé par ?portions=N. Zéro fait disparaître le choix : une recette
	// sans portions n'a pas de base à laquelle rapporter les quantités.
	Portions int

	Titre       string
	Image       string
	Faits       []fait
	Source      *source
	Ingredients []ligneDIngredient
	Etapes      []string
}

// fait est un couple libellé/valeur du bandeau : portions, durées, type de
// plat, saisons, tags, auteur. Une liste plutôt qu'un champ par donnée : le
// bandeau entier disparaît quand elle est vide, sans conteneur laissé vide.
type fait struct {
	Libelle string
	Valeur  string

	// Liens porte les valeurs qui mènent quelque part — le type de plat, les
	// saisons et les tags, qui renvoient tous à la liste filtrée. Le gabarit
	// rend celles-ci quand elles existent, et Valeur sinon : un fait est de
	// l'un ou l'autre genre, jamais des deux.
	Liens []lienDeFait
}

// lienDeFait est une valeur du bandeau doublée de sa destination. Texte reste
// ce qui se lit — le libellé du type de plat, le nom accentué de la saison, le
// nom du tag —, URL ce qui se tape.
type lienDeFait struct {
	URL   string
	Texte string
}

// source est le site d'où la recette vient.
//
// Nom est ce qui s'affiche, pas la colonne source_name : rien n'extrait le nom
// du site aujourd'hui, et le libellé se replie sur le domaine de l'adresse. Le
// calcul se fait ici, à l'affichage, et jamais en base — stocker le domaine
// dans source_name deviendrait faux le jour où le vrai nom est enfin extrait.
//
// URL peut être vide : une source de carnet ou de livre se lit sans qu'il y
// ait où aller.
type source struct {
	URL string
	Nom string
}

// ligneDIngredient porte les deux modes d'affichage d'une ligne.
//
// Brut est toujours renseigné — c'est le seul champ requis par le schéma — et
// sert de repli quand Aliment est vide. Le gabarit tranche sur Aliment, et rien
// d'autre : le schéma ne porte aucun indice de confiance, l'absence d'aliment
// reconnu est la seule traduction implémentable de « vide ou douteux ».
type ligneDIngredient struct {
	Brut       string
	Quantite   string
	Unite      string
	Aliment    string
	Note       string
	Facultatif bool
}

// pageRecette sert la fiche d'une recette.
//
// Le contrôle de session passe avant la recherche de l'enregistrement : sinon
// la différence entre un 404 et une redirection dirait à un visiteur quels
// identifiants existent. Cette route étant servie par notre code, les règles de
// collection ne la gardent pas — c'est à elle de vérifier la session.
//
// L'analyseur voyage par fermeture, depuis main() : la fiche accorde l'aliment
// à la quantité, et le pack comme le lexique sont chargés une fois au
// démarrage. Les relire ici les relirait à chaque page servie.
func pageRecette(a *analyseur) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			// La page de connexion de PATA-36, telle que main() la déclare et que
			// la mise en page la propose déjà au visiteur.
			return e.Redirect(http.StatusSeeOther, "/connexion")
		}

		recette, err := e.App.FindRecordById("recipes", e.Request.PathValue("id"))
		if err != nil {
			// Seul l'identifiant inconnu devient un 404 : une base injoignable
			// remonte comme erreur, sinon la page annoncerait une recette
			// supprimée à chaque incident.
			if errors.Is(err, sql.ErrNoRows) {
				return pageRecetteIntrouvable(e)
			}
			return err
		}

		donnees, err := ficheDeLaRecette(e.App, recette, e.Auth.Id, a, e.Request.URL.Query().Get("portions"))
		if err != nil {
			return err
		}

		// Les notes sont un bloc de la fiche : elles se lisent avec elle, et les
		// quatre routes de PATA-22 renvoient ce même bloc seul.
		notes, err := blocDesNotes(e, recette, "")
		if err != nil {
			return err
		}

		return rendre(e, "recette.html", "recette-corps.html", &donneesPage{
			Titre:        recette.GetString("title") + " — Patachoo",
			Recette:      donnees,
			Commentaires: notes,
		}, "commentaires.html")
	}
}

// PortionsMaximum expose la borne au gabarit, qui la pose sur le champ.
func (donneesRecette) PortionsMaximum() int {
	return portionsMaximum
}

// pageRecetteIntrouvable répond par une page lisible, et non par une 500 ni une
// page vide.
func pageRecetteIntrouvable(e *core.RequestEvent) error {
	return rendreAvecStatut(e, http.StatusNotFound,
		"recette-introuvable.html", "recette-introuvable-corps.html", &donneesPage{
			Titre: "Recette introuvable — Patachoo",
		})
}

// ficheDeLaRecette met la recette et ses ingrédients en forme pour le gabarit.
//
// compte est l'identifiant du compte connecté, et il décide du seul affichage
// conditionnel de la fiche. Un paramètre plutôt qu'un champ rempli chez les
// appelants, qui sont deux — pageRecette et rendLeBloc : la même règle posée
// deux fois finit par diverger.
//
// portions est le paramètre ?portions=N tel que reçu, ou "" : la fiche
// affiche alors les quantités de la recette. Rien n'en est écrit nulle part.
func ficheDeLaRecette(app core.App, recette *core.Record, compte string, a *analyseur, portions string) (*donneesRecette, error) {
	lignes, err := app.FindRecordsByFilter(
		"ingredients",
		"recipe = {:recette}",
		"position",
		0,
		0,
		dbx.Params{"recette": recette.Id},
	)
	if err != nil {
		return nil, fmt.Errorf("ingrédients de %s : %w", recette.Id, err)
	}

	if echecs := app.ExpandRecord(recette, []string{"meal_type", "tags", champAuteur}, nil); len(echecs) > 0 {
		return nil, fmt.Errorf("relations de %s : %v", recette.Id, echecs)
	}

	servings := recette.GetInt("servings")
	affichees := portionsAffichees(portions, servings)

	donnees := &donneesRecette{
		Id:          recette.Id,
		Sienne:      sienne(recette, compte),
		Portions:    affichees,
		Titre:       recette.GetString("title"),
		Image:       urlDeLaMiniature(recette),
		Faits:       faitsDeLaRecette(recette, affichees),
		Source:      sourceDeLaRecette(recette),
		Ingredients: ingredientsDeLaRecette(lignes, a, affichees, servings),
		Etapes:      etapes(recette.GetString("instructions")),
	}
	return donnees, nil
}

// faitsDeLaRecette rassemble le bandeau, dans l'ordre où il se lit. Un fait
// dont la valeur est vide n'entre pas dans la liste : c'est ainsi que le
// libellé disparaît avec la donnée.
//
// portions remplace servings dans le bandeau : c'est le nombre auquel les
// quantités affichées se rapportent.
func faitsDeLaRecette(recette *core.Record, portions int) []fait {
	var faits []fait
	ajoute := func(libelle, valeur string) {
		if valeur != "" {
			faits = append(faits, fait{Libelle: libelle, Valeur: valeur})
		}
	}

	if portions > 0 {
		ajoute("Portions", strconv.Itoa(portions))
	}
	ajoute("Temps de préparation", dureeLisible(recette.GetInt("prep_time")))
	ajoute("Temps de cuisson", dureeLisible(recette.GetInt("cook_time")))

	// Le type de plat est le seul fait cliquable : il mène à la liste des
	// recettes du même type. L'adresse s'appuie sur le slug, jamais sur le
	// libellé affiché ni sur l'identifiant PocketBase.
	if typeDePlat := recette.ExpandedOne("meal_type"); typeDePlat != nil {
		if nom := typeDePlat.GetString("name"); nom != "" {
			faits = append(faits, fait{
				Libelle: "Type de plat",
				Liens: []lienDeFait{{
					URL:   lienDeFiltreParType(typeDePlat),
					Texte: nom,
				}},
			})
		}
	}
	// seasons est un select, pas une relation : sa valeur se lit directement,
	// sans passer par l'expansion. Chaque saison mène à la liste filtrée, et
	// faute d'aucune le libellé disparaît comme celui de toute donnée
	// manquante.
	if liens := liensDesSaisons(recette.GetStringSlice("seasons")); len(liens) > 0 {
		faits = append(faits, fait{Libelle: "Saisons", Liens: liens})
	}

	// Les tags mènent à la liste filtrée pour la même raison : la fiche est
	// l'endroit où l'on découvre un tag, et donc celui d'où l'on veut voir ce
	// qu'il rassemble. Faute de tag, le libellé disparaît comme les autres.
	if liens := liensDesTags(recette.ExpandedAll("tags")); len(liens) > 0 {
		faits = append(faits, fait{Libelle: "Tags", Liens: liens})
	}

	// Le nom, et rien d'autre : l'auteur d'une recette est un autre compte que
	// son lecteur, et son courriel ne lui appartient pas. PocketBase le protège
	// partout ailleurs — users porte ViewRule = id = @request.auth.id et
	// emailVisibility vaut faux —, mais l'expansion de created_by passe à côté
	// de la règle. Faute de nom, le bloc disparaît, comme toute donnée
	// manquante.
	if auteur := recette.ExpandedOne(champAuteur); auteur != nil {
		ajoute("Ajoutée par", strings.TrimSpace(auteur.GetString("name")))
	}

	return faits
}

// liensDesSaisons traduit les saisons stockées en liens vers la liste filtrée.
//
// Une valeur que la table ne connaît pas est écartée plutôt que rendue sans
// lien : le schéma restreint le champ aux quatre valeurs de la table, et un
// test interdit que les deux divergent. Fabriquer un lien vers une saison qui
// n'existe pas serait pire que de ne rien afficher.
func liensDesSaisons(saisons []string) []lienDeFait {
	var liens []lienDeFait
	for _, saison := range saisons {
		if adresse := urlDeLaSaison(saison); adresse != "" {
			liens = append(liens, lienDeFait{URL: "/recettes?saison=" + adresse, Texte: saison})
		}
	}
	return liens
}

// sourceDeLaRecette rend le bloc de source, ou nil : les deux champs sont vides
// sur une saisie manuelle, et c'est le cas normal.
//
// Un seul des deux suffit à faire un bloc. C'est le libellé qui décide : faute
// de quoi que ce soit à écrire, il n'y a ni bloc, ni ponctuation orpheline.
func sourceDeLaRecette(recette *core.Record) *source {
	adresse := strings.TrimSpace(recette.GetString("source_url"))
	nom := libelleSource(recette.GetString("source_name"), adresse)
	if nom == "" {
		return nil
	}
	return &source{URL: adresse, Nom: nom}
}

// libelleSource dit sous quel nom la source s'affiche.
//
// Dans l'ordre : le nom du site s'il est renseigné ; sinon l'hôte de l'adresse,
// sans son www. et en minuscules — c'est le cas courant, puisque rien
// n'extrait le nom du site aujourd'hui ; sinon l'adresse telle quelle.
//
// Ce dernier repli est délibéré : une adresse dont on ne tire aucun hôte donne
// un libellé laid, mais la source ne disparaît pas. C'est la même règle que le
// repli d'un ingrédient sur sa ligne brute.
func libelleSource(nom, adresse string) string {
	if nom := strings.TrimSpace(nom); nom != "" {
		return nom
	}

	adresse = strings.TrimSpace(adresse)
	if adresse == "" {
		return ""
	}

	// url.Parse ne rejette presque rien : « pas-une-url » lui passe pour un
	// chemin relatif, et « javascript:alert(1) » pour une URL opaque. Les deux
	// rendent un hôte vide, et c'est là-dessus que le repli se décide — pas sur
	// l'erreur, qui ne vient qu'exceptionnellement.
	if analysee, err := url.Parse(adresse); err == nil {
		if hote := analysee.Hostname(); hote != "" {
			return strings.TrimPrefix(strings.ToLower(hote), "www.")
		}
	}
	return adresse
}

// ingredientsDeLaRecette met chaque ligne en forme, dans l'ordre reçu.
//
// Aliment n'est pas food tel quel : la colonne porte la forme canonique du
// lexique, donc le singulier, et la fiche l'accorde à la quantité (PATA-116).
//
// La quantité est rapportée aux portions affichées avant d'être mise en forme
// et accordée : le pluriel suit ce qu'on lit, pas ce qui est stocké.
func ingredientsDeLaRecette(lignes []*core.Record, a *analyseur, portions, servings int) []ligneDIngredient {
	rendues := make([]ligneDIngredient, 0, len(lignes))
	for _, ligne := range lignes {
		quantite := quantiteProportionnelle(ligne.GetFloat("quantity"), portions, servings)
		rendues = append(rendues, ligneDIngredient{
			Brut:       ligne.GetString("raw"),
			Quantite:   quantiteLisible(quantite),
			Unite:      strings.TrimSpace(ligne.GetString("unit")),
			Aliment:    a.accorde(strings.TrimSpace(ligne.GetString("food")), quantite),
			Note:       strings.TrimSpace(ligne.GetString("note")),
			Facultatif: ligne.GetBool("optional"),
		})
	}
	return rendues
}

// urlDeLaMiniature rend l'adresse de la vignette, ou "" faute d'image.
//
// Le nom de la collection plutôt que son identifiant : PocketBase accepte les
// deux, et l'adresse reste lisible dans une page comme dans un journal.
func urlDeLaMiniature(recette *core.Record) string {
	fichier := recette.GetString("image")
	if fichier == "" {
		return ""
	}
	return fmt.Sprintf("/api/files/%s/%s/%s?thumb=%s",
		recette.Collection().Name, recette.Id, url.PathEscape(fichier), miniatureDeLaFiche)
}

// etapes découpe les instructions en une étape par ligne non vide.
//
// instructions porte du texte, pas du HTML — tranché le 19/08/2026 : le gabarit
// l'échappe, et les lignes vides séparent sans produire d'étape vide.
func etapes(instructions string) []string {
	var decoupees []string
	for _, ligne := range strings.Split(instructions, "\n") {
		if ligne := strings.TrimSpace(ligne); ligne != "" {
			decoupees = append(decoupees, ligne)
		}
	}
	return decoupees
}

// dureeLisible met des minutes en français : « 45 min », « 1 h 30 », « 2 h ».
//
// Le zéro de tête de « 1 h 05 » n'est pas un ornement : « 1 h 5 » se lit comme
// une coquille, et la fiche est faite pour être lue en cuisinant.
//
// Zéro ou négatif rend "", et c'est le bloc entier qui disparaît : une durée
// absente n'est pas une durée nulle.
func dureeLisible(minutes int) string {
	switch {
	case minutes <= 0:
		return ""
	case minutes < 60:
		return fmt.Sprintf("%d min", minutes)
	case minutes%60 == 0:
		return fmt.Sprintf("%d h", minutes/60)
	default:
		return fmt.Sprintf("%d h %02d", minutes/60, minutes%60)
	}
}

// quantiteLisible rend la quantité sans zéro inutile, virgule décimale
// comprise : 200 donne « 200 », 0.5 donne « 0,5 ». Zéro rend "" — le champ est
// facultatif, et le parser le laisse vide sur « une pincée de sel ».
func quantiteLisible(quantite float64) string {
	if quantite == 0 {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(quantite, 'f', -1, 64), ".", ",", 1)
}

// portionsAffichees dit à combien de portions la fiche se rapporte : celles
// demandées si la valeur se lit, celles de la recette sinon.
//
// Une valeur illisible, nulle, négative ou au-delà de portionsMaximum est
// ignorée plutôt que refusée : c'est un réglage de lecture, et une adresse
// mal tapée doit encore montrer la recette. Sans portions de référence, rien
// n'est demandable.
func portionsAffichees(demande string, servings int) int {
	portions, err := strconv.Atoi(demande)
	if servings <= 0 || err != nil || portions < 1 || portions > portionsMaximum {
		return servings
	}
	return portions
}

// quantiteProportionnelle rapporte la quantité aux portions demandées,
// arrondie à deux décimales.
//
// Rien ne bouge quand les portions sont celles de la recette, pas même
// l'arrondi : la fiche sans paramètre reste celle d'avant PATA-138. Une
// quantité absente le reste, et une quantité que l'arrondi effacerait
// s'affiche au plus petit pas plutôt que de disparaître.
func quantiteProportionnelle(quantite float64, portions, servings int) float64 {
	if quantite == 0 || portions == servings {
		return quantite
	}
	arrondie := math.Round(quantite*float64(portions)/float64(servings)*100) / 100
	if arrondie == 0 {
		return 0.01
	}
	return arrondie
}
