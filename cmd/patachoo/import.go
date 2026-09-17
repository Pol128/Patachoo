package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	balisage "golang.org/x/net/html"

	"github.com/Pol128/Patachoo/jsonld"
	"github.com/Pol128/Patachoo/recuperation"
)

// L'import par URL : l'adresse entre, une fiche pré-remplie sort.
//
// Rien n'est écrit en base sur ce chemin, sur aucun des deux parcours. La
// collection n'a pas de champ « brouillon » et n'en a pas besoin : l'aperçu
// est un formulaire pré-rempli, et c'est l'utilisateur qui le valide, sur
// POST /recettes.
//
// Le parcours d'échec est la moitié du travail. Un site sur quatre ne publie
// pas de JSON-LD exploitable, et ce jour-là l'utilisateur doit lire ce qui
// s'est passé — pas « une erreur est survenue » — et retrouver le formulaire
// garni de ce qui a pu être trouvé.

// recuperePage va chercher la page distante.
//
// Une variable, et non un appel direct à recuperation.Recupere : c'est le seul
// point où les tests peuvent piéger le réseau pour vérifier qu'aucune requête
// ne part. Le délai, la taille maximale, les redirections et le refus des
// adresses non routables sont l'affaire de recuperation, jamais réécrits ici.
var recuperePage = func(ctx context.Context, adresse string, choix ...recuperation.Option) (recuperation.Page, error) {
	return recuperation.Recupere(ctx, adresse, choix...)
}

// brancheLImport pose les deux routes de l'import, toutes deux derrière la
// session.
//
// L'import déclenche une requête sortante depuis le serveur vers une URL
// fournie par l'appelant : une route ouverte offrirait ce mandataire à qui la
// trouve. Le contrôle passe donc avant tout le reste — avant la lecture du
// champ, avant le moindre appel.
func brancheLImport(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET("/recettes/importer", pageImport).Bind(exigeUneSession())
	routeur.POST("/recettes/importer", importe).Bind(exigeLeJetonAntiRejeu(), exigeUneSession(),
		rendLeDepassementEnHTML("patachooDepassementImport", rendLeDepassementDeLImport))
}

// donneesImport est ce que la page « coller l'URL » donne à son gabarit. URL
// est ce qui a été soumis : après un refus, le champ se rend avec ce que
// l'utilisateur a tapé, pas vide.
type donneesImport struct {
	donneesPage
	URL string
}

// pageImport rend le champ « coller l'URL ».
func pageImport(e *core.RequestEvent) error {
	return rendLaPageDImport(e, "", "")
}

// importe reçoit l'adresse collée et rend la fiche pré-remplie.
func importe(e *core.RequestEvent) error {
	champs, err := valeursSoumises(e)
	if err != nil {
		return err
	}
	adresse := strings.TrimSpace(champs.Get("url"))

	if refus := refusDeLAdresse(adresse); refus != "" {
		// Le champ se re-rend avec son message, et rien n'est parti : la
		// validation de la saisie précède le réseau.
		return rendLaPageDImport(e, adresse, refus)
	}

	trouve, message, renonce := ceQuiAEteTrouve(e.Request.Context(), adresse)
	if renonce {
		// Rien n'a été lu, et il n'y a pas de fiche à proposer : le champ se
		// re-rend avec l'adresse et le message, comme après un refus de saisie.
		// Un formulaire vide donnerait à croire que le site n'a rien publié.
		return rendLaPageDImport(e, adresse, message)
	}

	saisie, err := formulaireVide(e.App)
	if err != nil {
		return err
	}
	saisie.Legende = "Vérifier la recette importée"
	// Le formulaire de PATA-15, et sa route d'enregistrement : l'import
	// s'arrête au formulaire rendu.
	saisie.Action = "/recettes"
	saisie.Titre = trouve.Titre
	saisie.Portions = trouve.Portions
	saisie.TempsPreparation = trouve.TempsPreparation
	saisie.TempsCuisson = trouve.TempsCuisson
	saisie.Instructions = trouve.Instructions
	saisie.Ingredients = trouve.Ingredients
	saisie.ImageDistante = trouve.ImageDistante
	saisie.SourceURL = trouve.SourceURL
	saisie.SourceNom = trouve.SourceNom

	return rendLeFormulaire(e, saisie, message)
}

// rendLaPageDImport rend le champ « coller l'URL », avec un message s'il y en
// a un.
func rendLaPageDImport(e *core.RequestEvent, adresse, message string) error {
	return rendLaPageDImportAvecStatut(e, http.StatusOK, adresse, message)
}

// rendLaPageDImportAvecStatut rend la même page sous un autre code de retour.
//
// Le dépassement du plafond en a besoin : un refus du limiteur rendu 200 ferait
// passer pour une page valide ce que le protocole doit signaler comme un refus.
func rendLaPageDImportAvecStatut(e *core.RequestEvent, statut int, adresse, message string) error {
	return rendreAvecStatut(e, statut, "import.html", "import-corps.html", &donneesImport{
		donneesPage: donneesPage{Titre: "Importer une recette — Patachoo", Message: message},
		URL:         adresse,
	})
}

// messageDebitDeLImportDepasse est ce que voit celui qui a dépassé le plafond
// de débit posé par la migration 1789660000_debit_import.
//
// Il parle de l'adresse, et non du compte : le limiteur compte par e.RealIP()
// quelle que soit l'audience de la règle, et promettre un plafond par compte
// serait promettre ce que le code ne fait pas.
const messageDebitDeLImportDepasse = "Trop d'imports lancés depuis cette adresse. Réessayez dans une minute."

// rendLeDepassementDeLImport est ce que rendLeDepassementEnHTML rend quand le
// plafond de POST /recettes/importer tombe : le champ « coller l'URL »
// lui-même, sous un 429.
//
// L'adresse est reprise de la requête refusée : « le champ se re-rend avec ce
// que l'utilisateur a tapé » vaut aussi quand c'est le plafond qui refuse, et
// importe n'a jamais été appelé pour la lire. Au-delà de borneDuCorpsRattrape
// la lecture échoue et le champ revient vide — le refus reste lisible, c'est ce
// qui compte.
func rendLeDepassementDeLImport(e *core.RequestEvent) error {
	return rendLaPageDImportAvecStatut(e, http.StatusTooManyRequests,
		e.Request.PostFormValue("url"), messageDebitDeLImportDepasse)
}

// refusDeLAdresse dit pourquoi une adresse ne mérite pas qu'on aille la
// chercher, ou "" si elle est acceptable.
//
// Ce n'est pas la politique de sécurité de recuperation, qui juge l'adresse
// résolue et vaut aussi après une redirection : c'est la validation de la
// saisie, qui évite d'ouvrir une connexion pour un champ vide ou un copier-
// coller tronqué.
//
// Les trois messages vivent en constantes plutôt qu'en littéraux au fil du
// code, pour la même raison que le disclaimer du lot : ce qui est promis à
// l'utilisateur doit être nommable par un test.
const (
	refusSaisieAbsente  = "Collez l'adresse de la page de la recette."
	refusPasUnePage     = "Cette adresse n'est pas celle d'une page : il y manque le début, « https:// » et le nom du site."
	refusSchemaNonSuivi = "Nous n'allons chercher que des pages web, en http:// ou en https://."
)

func refusDeLAdresse(adresse string) string {
	if adresse == "" {
		return refusSaisieAbsente
	}

	cible, err := url.Parse(adresse)
	if err != nil || !cible.IsAbs() {
		return refusPasUnePage
	}

	// Le schéma se juge avant l'hôte : file:///etc/passwd n'a pas d'hôte, mais
	// il porte bien un début d'adresse. Lui répondre qu'il en manque un serait
	// faux, et c'est précisément le schéma qu'il faut nommer ici.
	if cible.Scheme != "http" && cible.Scheme != "https" {
		return refusSchemaNonSuivi
	}
	if cible.Host == "" {
		return refusPasUnePage
	}
	return ""
}

// preRemplissage est ce qu'un import a pu trouver.
//
// Les champs vides sont des absences réelles : une page qui ne publie rien
// laisse le formulaire vide, elle ne l'invente pas. Les nombres y sont des
// chaînes, comme dans le formulaire : c'est ce qui permet à un champ non
// renseigné de rester vide plutôt que d'afficher 0.
type preRemplissage struct {
	Titre            string
	Portions         string
	TempsPreparation string
	TempsCuisson     string
	Instructions     string
	Ingredients      string
	ImageDistante    string
	SourceURL        string
	SourceNom        string
}

// ceQuiAEteTrouve va chercher la page et en tire ce qu'elle publie.
//
// Elle rend toujours de quoi remplir le formulaire — au pire l'adresse seule —
// et le message qui nomme la cause quand quelque chose a manqué. Jamais
// d'erreur : un import raté n'est pas une panne, c'est le parcours d'échec.
//
// Le troisième retour distingue le seul échec qui ne soit pas un parcours
// d'échec : l'appel a renoncé à attendre le tour de l'hôte, rien n'est parti,
// et il n'y a donc aucune fiche à proposer — pas même une fiche vide, qui
// donnerait à croire que le site a été lu et n'a rien publié.
func ceQuiAEteTrouve(ctx context.Context, adresse string) (preRemplissage, string, bool) {
	// La cadence de l'instance, celle-là même que l'ouvrier du lot emprunte :
	// l'import unitaire sort sur le réseau sous notre adresse et sous notre
	// nom, et le rythme que le lot promet ne vaudrait rien s'il suffisait de
	// coller une URL en boucle pour en sortir.
	page, err := recuperePage(ctx, adresse,
		recuperation.AvecCadence(cadenceDeRecuperation{cadenceDeLInstance}),
		recuperation.AvecAttenteMaxDeCadence(attenteMaxUnitaire))
	if err != nil {
		// La page n'a pas été atteinte : il n'y a rien à lire, pas même ses
		// balises Open Graph. Reste l'adresse collée.
		return preRemplissage{SourceURL: adresse}, messageDeLEchec(err), estUneAttenteAbandonnee(err)
	}

	ouverture := litOpenGraph(page.Corps)
	trouve := preRemplissage{
		// L'URL finale, celle du dernier saut : c'est elle qui désigne la
		// recette, pas le lien raccourci par lequel on y est arrivé.
		SourceURL: page.URLFinale,
		SourceNom: nomDuSite(ouverture.NomDuSite, page.URLFinale),
	}

	recette, err := jsonld.Extraire(page.Corps)
	if err != nil {
		// Open Graph donne souvent le titre et l'image, et c'est beaucoup à
		// qui devrait sinon tout retaper.
		trouve.Titre = ouverture.Titre
		trouve.ImageDistante = ouverture.Image
		return trouve, messageDeLEchec(err), false
	}

	champs := preRemplissageDe(recette)
	champs.SourceURL = trouve.SourceURL
	champs.SourceNom = trouve.SourceNom
	return champs, "", false
}

// estUneAttenteAbandonnee dit si l'échec est un renoncement à attendre le tour
// de l'hôte. C'est la cause nommée qui le dit, jamais le texte de l'erreur.
func estUneAttenteAbandonnee(err error) bool {
	var refus *recuperation.Erreur
	return errors.As(err, &refus) && refus.Cause == recuperation.AttenteDeCadence
}

// preRemplissageDe rend, d'une recette extraite, les champs qu'elle garnit.
//
// C'est la correspondance JSON-LD → recipes, et elle n'est écrite qu'une fois :
// l'import unitaire la rend dans un formulaire à valider, l'import en lot
// (PATA-42) l'enregistre sans passer par personne. Deux recettes du même
// calcul divergeraient au premier cas particulier.
//
// La source n'en fait pas partie : elle vient de la page récupérée, pas de son
// balisage, et l'appelant la pose lui-même.
func preRemplissageDe(recette jsonld.Recette) preRemplissage {
	// La description est lue par l'extraction et ignorée ici : la collection
	// n'a pas de champ pour elle, et on ne la mélange pas aux instructions.
	return preRemplissage{
		Titre:            recette.Titre,
		Portions:         premierEntier(recette.Portions),
		TempsPreparation: minutesAffichees(recette.PreparationMin),
		TempsCuisson:     minutesAffichees(recette.CuissonMin),
		Instructions:     strings.Join(recette.Etapes, "\n"),
		Ingredients:      strings.Join(recette.Ingredients, "\n"),
		// L'image est montrée à distance, pas attachée : le téléchargement est
		// PATA-10, et une recette importée d'ici là est enregistrée sans image.
		ImageDistante: recette.Image,
	}
}

// nomDuSite retient le nom que le site se donne, ou son hôte à défaut.
func nomDuSite(publie, adresse string) string {
	if publie != "" {
		return publie
	}
	if cible, err := url.Parse(adresse); err == nil {
		return cible.Hostname()
	}
	return ""
}

// premierEntier réduit un rendement à son premier nombre : « 4 personnes »
// vaut 4.
//
// Un rendement sans chiffre — « un grand bocal » — laisse le champ vide plutôt
// qu'à 0 : « 0 portion » est une information, et elle serait fausse.
func premierEntier(mention string) string {
	debut := strings.IndexAny(mention, "0123456789")
	if debut < 0 {
		return ""
	}

	fin := debut
	for fin < len(mention) && mention[fin] >= '0' && mention[fin] <= '9' {
		fin++
	}
	return mention[debut:fin]
}

// minutesAffichees rend la durée telle qu'elle se retape, et la chaîne vide
// pour une durée que le site n'a pas publiée.
func minutesAffichees(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	return strconv.Itoa(minutes)
}

// --- Les messages d'échec ---------------------------------------------------

// Les causes n'appellent pas la même réaction de l'utilisateur : un site
// injoignable se réessaie, un site qui bloque les robots se recopie à la main,
// un site sans balisage ne s'importera jamais. Chaque message dit laquelle.
const (
	messageInjoignable     = "Ce site est injoignable : son adresse ne répond pas, ou pas assez vite. Rien n'a pu être lu."
	messagePolitique       = "Cette adresse a été refusée : elle vise un réseau que nous n'allons pas chercher, ou le site nous interdit sa lecture par son robots.txt."
	messageTropVolumineuse = "Cette page est trop volumineuse pour être lue en entier : l'import s'est arrêté avant d'y trouver quoi que ce soit."
	messageSansRecette     = "Ce site publie bien des données structurées, mais aucune recette : il n'y a rien à en tirer automatiquement."
	messageAucunBalisage   = "Ce site ne publie pas ses recettes dans un format exploitable."
	messageJSONInvalide    = "Ce site publie des données structurées illisibles : son balisage est cassé."
	messageTitreAbsent     = "La recette publiée par ce site n'a pas de titre, et une fiche sans titre n'en est pas une."
	// messageSiteOccupe n'est pas un échec du site : c'est nous qui avons
	// renoncé à attendre notre tour vers lui.
	//
	// Il ne nomme pas ce qui éloigne ce tour, parce que le code ne le sait pas :
	// recuperation.AttenteDeCadence ne distingue pas un import en lot qui
	// parcourt l'hôte de l'hôte qui réclame lui-même de longs délais. Et il ne
	// promet pas qu'un nouvel essai aboutira — sur un Crawl-delay long, rien ne
	// périme l'annonce et tous les essais suivants renonceront au même endroit.
	// Ce qu'il offre à la place est la seule sortie qui existe vraiment :
	// l'import en lot, qui attend son tour sans limite.
	messageSiteOccupe = "Nous espaçons nos visites à un même site, et notre prochain tour vers celui-ci est plus loin que ce qu'un import à la main accepte d'attendre : rien n'a été lu. Un import en lot, lui, patientera le temps qu'il faudra."
)

// messageDeLEchec nomme la cause dans les mots de l'utilisateur.
//
// Le tri se fait sur les causes nommées de recuperation et de jsonld, jamais
// sur le texte de l'erreur : un message technique ne doit ni décider de ce qui
// s'affiche, ni s'y retrouver.
func messageDeLEchec(err error) string {
	var refus *recuperation.Erreur
	if errors.As(err, &refus) {
		return messageDuRefus(refus)
	}

	switch {
	case errors.Is(err, jsonld.ErrSansRecette):
		return messageSansRecette
	case errors.Is(err, jsonld.ErrAucunBalisage):
		return messageAucunBalisage
	case errors.Is(err, jsonld.ErrJSONInvalide):
		return messageJSONInvalide
	case errors.Is(err, jsonld.ErrTitreAbsent):
		return messageTitreAbsent
	}
	// Une erreur qu'aucune cause nommée ne couvre : nous n'en savons pas plus
	// que « la page n'a pas été atteinte », et son texte reste où il est.
	return messageInjoignable
}

// messageDuRefus traduit les causes de recuperation. Le délai dépassé rejoint
// l'injoignable, et le robots.txt le refus de politique : ce sont deux
// manières de ne pas avoir la page, et l'utilisateur n'a rien à faire de la
// nuance.
func messageDuRefus(refus *recuperation.Erreur) string {
	switch refus.Cause {
	case recuperation.RefusHTTP:
		return fmt.Sprintf("Ce site a refusé notre visite (code %d) : beaucoup de sites bloquent les robots, et celui-ci en fait partie.", refus.Code)
	case recuperation.RefuseeParPolitique, recuperation.RobotsInterdit:
		return messagePolitique
	case recuperation.TailleMax:
		return messageTropVolumineuse
	case recuperation.AttenteDeCadence:
		return messageSiteOccupe
	default:
		return messageInjoignable
	}
}

// --- Open Graph -------------------------------------------------------------

// ouverture est ce que les balises Open Graph d'une page publient.
//
// Les lire appartient à l'import : jsonld ne lit que le JSON-LD, et ce
// repli-ci ne sert qu'au parcours d'échec — plus og:site_name, qui nomme la
// source même quand tout s'est bien passé.
type ouverture struct {
	Titre     string
	Image     string
	NomDuSite string
}

// litOpenGraph lit og:title, og:image et og:site_name, et retombe sur la
// balise <title> à défaut d'og:title.
//
// Un vrai parseur, comme pour les blocs ld+json : les pages réelles portent
// leurs attributs dans n'importe quel ordre et en guillemets simples, autant
// d'endroits où une expression régulière se trompe. Le parcours est itératif :
// une page à l'imbrication démesurée ne doit pas faire déborder la pile.
func litOpenGraph(page []byte) ouverture {
	racine, err := balisage.Parse(bytes.NewReader(page))
	if err != nil {
		return ouverture{}
	}

	var lue ouverture
	var titreDuDocument string

	aVoir := []*balisage.Node{racine}
	for len(aVoir) > 0 {
		noeud := aVoir[len(aVoir)-1]
		aVoir = aVoir[:len(aVoir)-1]

		if noeud.Type == balisage.ElementNode {
			switch noeud.Data {
			case "meta":
				switch attribut(noeud, "property") {
				case "og:title":
					lue.Titre = attribut(noeud, "content")
				case "og:image":
					lue.Image = attribut(noeud, "content")
				case "og:site_name":
					lue.NomDuSite = attribut(noeud, "content")
				}
			case "title":
				if titreDuDocument == "" {
					titreDuDocument = strings.TrimSpace(texteDuNoeud(noeud))
				}
			}
		}
		// Empilés à l'envers pour être dépilés dans l'ordre du document.
		for enfant := noeud.LastChild; enfant != nil; enfant = enfant.PrevSibling {
			aVoir = append(aVoir, enfant)
		}
	}

	if lue.Titre == "" {
		lue.Titre = titreDuDocument
	}
	return lue
}

// attribut rend la valeur d'un attribut, rognée. Le parseur en a déjà décodé
// les entités : le contenu arrive tel que l'utilisateur doit le lire.
func attribut(noeud *balisage.Node, nom string) string {
	for _, a := range noeud.Attr {
		if a.Key == nom {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

// texteDuNoeud rend le texte direct d'un élément.
func texteDuNoeud(noeud *balisage.Node) string {
	var texte strings.Builder
	for enfant := noeud.FirstChild; enfant != nil; enfant = enfant.NextSibling {
		if enfant.Type == balisage.TextNode {
			texte.WriteString(enfant.Data)
		}
	}
	return texte.String()
}
