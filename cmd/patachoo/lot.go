package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/Pol128/Patachoo/internal/texte"
)

// cheminDuLot porte les deux routes de la fournée, dans le prolongement de
// /recettes/importer (PATA-9) : le GET rend la page de saisie, le POST crée le
// lot. Une seule constante, parce qu'une URL recopiée finit par diverger de
// celle qui est branchée.
const cheminDuLot = "/recettes/importer/lot"

// plafondDuLot borne le nombre d'URLs retenues dans une fournée.
//
// Au-delà, le lot est refusé en entier plutôt que tronqué : une liste rognée
// en silence ferait croire que tout est parti, et personne ne saurait dire où
// la coupe a eu lieu.
const plafondDuLot = 500

// fourneesMaxParMinute borne la recherche d'un nom de tag libre.
//
// Le nom du lot porte la minute ; deux lots de la même minute se distinguent
// par un discriminant, cherché de proche en proche. Sans borne, une base dont
// les tags seraient tous pris ferait boucler la requête indéfiniment.
const fourneesMaxParMinute = 100

// messageDebitDuLotDepasse est ce que voit celui qui a dépassé le plafond de
// débit posé par la migration 1789158900_debit_lot.
//
// Il parle de l'adresse, et non du compte : le limiteur compte par e.RealIP()
// quelle que soit l'audience de la règle, et promettre un plafond par compte
// serait promettre ce que le code ne fait pas.
const messageDebitDuLotDepasse = "Trop de lots lancés depuis cette adresse. Réessayez dans une minute."

// messageFourneesEpuisees est ce que voit celui qui tombe sur une minute dont
// tous les noms de fournée sont pris.
//
// Distinct du précédent, et volontairement : les deux disent d'attendre une
// minute, mais l'un vient du plafond de débit et l'autre de la liste des tags.
// Un message unique rendrait le second indiscernable du premier dans un
// rapport de bogue.
const messageFourneesEpuisees = "Trop de lots ont déjà été lancés pendant cette minute. Réessayez dans une minute."

// erreurFourneesEpuisees marque le seul échec de creeLeLot qui se traduise en
// page : les fourneesMaxParMinute noms de la minute sont pris.
//
// Un sentinel plutôt qu'un rendu fait sur place, comme erreurIntrouvable
// (commentaires.go) : creeLeLot ne choisit pas ce que la route affiche. Et un
// sentinel plutôt que le texte de l'erreur, parce que rendre err.Error() tel
// quel afficherait aussi les échecs d'écriture — un message de la couche SQL
// n'a rien à faire dans du HTML, il dit la forme des tables à qui le lit.
var erreurFourneesEpuisees = errors.New("tous les noms de fournée de la minute sont pris")

// disclaimerDuLot : les trois phrases affichées juste au-dessus du bouton qui
// lance la fournée, et non dans une page d'aide que personne n'ouvre. Elles
// vivent ici plutôt que dans le gabarit pour que ce qui est promis à
// l'utilisateur soit vérifiable par un test.
var disclaimerDuLot = []string{
	"Tenez-vous-en à des volumes raisonnables : un lot n'est pas un aspirateur.",
	"Le robots.txt des sites visités est respecté.",
	"Ce qui est récolté reste sur cette instance, et n'est redistribué nulle part.",
}

// ligneEcartee est une ligne de la saisie qui ne partira pas, et ce qu'on lui
// reproche. Les deux ensemble : un refus sans motif est une panne du point de
// vue de celui qui a collé la liste.
type ligneEcartee struct {
	Ligne string
	Motif string
}

// donneesLot est ce que les gabarits du lot reçoivent — la page de saisie
// comme la confirmation.
type donneesLot struct {
	donneesPage
	Disclaimer []string
	Saisie     string
	Ecartees   []ligneEcartee

	// Suivi est le fragment vivant que la confirmation porte, et il n'est
	// rempli que par elle : c'est le point d'accroche par lequel la
	// progression, puis le rapport, prennent la place de « lot lancé ».
	Suivi *donneesSuivi
}

// brancheLImportEnLot pose les trois routes de la fournée : la saisie, le
// lancement, et le suivi (rapport.go).
//
// Toutes derrière exigeUneSession, comme les routes du formulaire : elles
// rendent des pages et écrivent en base, et le contrôle passe avant la lecture
// de la saisie comme avant celle du lot.
func brancheLImportEnLot(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET(cheminDuLot, pageImportEnLot).Bind(exigeUneSession())
	routeur.POST(cheminDuLot, lanceLeLot).Bind(exigeUneSession(),
		rendLeDepassementEnHTML("patachooDepassementLot", rendLeDepassementDuLot))
	routeur.GET(cheminDuSuivi, suiviDuLot).Bind(exigeUneSession())
}

// pageImportEnLot rend la page de saisie vide.
func pageImportEnLot(e *core.RequestEvent) error {
	return rendLaSaisie(e, "", "")
}

// rendLaSaisie rend la page de saisie, avec un message s'il y en a un et la
// liste telle qu'elle a été collée : un lot refusé ne doit pas se retaper.
func rendLaSaisie(e *core.RequestEvent, saisie, message string) error {
	return rendLaSaisieAvecStatut(e, http.StatusOK, saisie, message)
}

// rendLaSaisieAvecStatut rend la même page sous un autre code de retour.
//
// Le dépassement du plafond en a besoin : un refus du limiteur rendu 200 ferait
// passer pour une page valide ce que le protocole doit signaler comme un refus.
func rendLaSaisieAvecStatut(e *core.RequestEvent, statut int, saisie, message string) error {
	return rendreAvecStatut(e, statut, "import-lot.html", "import-lot-corps.html", &donneesLot{
		donneesPage: donneesPage{Titre: "Import en lot — Patachoo", Message: message},
		Disclaimer:  disclaimerDuLot,
		Saisie:      saisie,
	})
}

// rendLeDepassementDuLot est ce que rendLeDepassementEnHTML rend quand le
// plafond de POST /recettes/importer/lot tombe : la page de saisie elle-même,
// sous un 429.
//
// La saisie est reprise de la requête refusée : « un lot refusé ne doit pas se
// retaper » vaut aussi quand c'est le plafond qui refuse, et lanceLeLot n'a
// jamais été appelé pour la lire. Au-delà de borneDuCorpsRattrape la lecture
// échoue et le champ revient vide — le refus reste lisible, c'est ce qui
// compte.
func rendLeDepassementDuLot(e *core.RequestEvent) error {
	return rendLaSaisieAvecStatut(e, http.StatusTooManyRequests,
		e.Request.PostFormValue("urls"), messageDebitDuLotDepasse)
}

// lanceLeLot valide la liste collée, puis écrit la fournée.
//
// Rien ne sort sur le réseau ici : cette route remplit la file, c'est l'ouvrier
// de PATA-42 qui la consomme.
func lanceLeLot(e *core.RequestEvent) error {
	saisie := e.Request.PostFormValue("urls")

	// La validation d'abord, et l'écriture ensuite : un lot refusé ne doit
	// laisser ni tag, ni enregistrement imports derrière lui.
	retenues, ecartees, err := analyseLaSaisie(saisie)
	if err != nil {
		return rendLaSaisie(e, saisie, err.Error())
	}

	// Le tag n'est pas relu ici : c'est le suivi qui le nomme, et qui y
	// renvoie.
	lot, _, err := creeLeLot(e.App, e.Auth, retenues, time.Now())
	if err != nil {
		// Les noms de fournée épuisés sont un refus de saisie comme les
		// autres, et le seul de creeLeLot à en être un : tout le reste remonte
		// tel quel, et ErrorHandler s'en charge.
		if errors.Is(err, erreurFourneesEpuisees) {
			return rendLaSaisie(e, saisie, messageFourneesEpuisees)
		}
		return err
	}

	// Le suivi est rendu ici plutôt que redemandé par une première requête
	// HTMX : sans lui, la page resterait vide jusqu'au premier
	// rafraîchissement. Ce qui reste de la confirmation, ce sont les lignes
	// écartées — elles parlent de la saisie, pas de la fournée, et le suivi
	// n'en saura jamais rien.
	suivi, err := suiviDe(e.App, lot)
	if err != nil {
		return err
	}

	return rendre(e, "import-lot-resultat.html", "import-lot-resultat-corps.html", &donneesLot{
		donneesPage: donneesPage{Titre: "Import en lot — Patachoo"},
		Ecartees:    ecartees,
		Suivi:       suivi,
	}, "import-lot-suivi-corps.html")
}

// analyseLaSaisie filtre la liste collée : une URL par ligne.
//
// Elle rend ce qui part, ce qui est écarté, et une erreur quand le lot entier
// est refusé. Les deux premiers vont ensemble : une ligne fautive n'empêche pas
// les autres de partir, elle est simplement listée.
func analyseLaSaisie(saisie string) ([]string, []ligneEcartee, error) {
	var retenues []string
	var ecartees []ligneEcartee
	vues := map[string]bool{}

	for _, brute := range strings.Split(saisie, "\n") {
		ligne := strings.TrimSpace(brute)
		// Une ligne vide n'est pas une erreur : c'est un retour à la ligne de
		// trop dans un copier-coller, pas une URL manquée.
		if ligne == "" {
			continue
		}

		if motif := refusDeLaLigneDuLot(ligne); motif != "" {
			ecartees = append(ecartees, ligneEcartee{Ligne: ligne, Motif: motif})
			continue
		}

		// Sur l'URL telle quelle, et sans normalisation : l'index unique
		// (batch, url) posé par la sous-tâche 1 est le filet, pas le filtre, et
		// c'est l'adresse tapée que l'utilisateur reconnaîtra dans le rapport.
		if vues[ligne] {
			continue
		}
		vues[ligne] = true
		retenues = append(retenues, ligne)
	}

	if len(retenues) > plafondDuLot {
		return nil, nil, fmt.Errorf(
			"%d URLs retenues, %d au maximum : le lot n'a pas été créé",
			len(retenues), plafondDuLot)
	}
	// Un lot sans rien à faire n'est pas un lot : il laisserait un tag orphelin
	// et un enregistrement que la sous-tâche 3 n'aurait jamais à reprendre.
	if len(retenues) == 0 {
		return nil, nil, errors.New(
			"aucune URL retenue : chaque ligne doit porter une adresse http ou https complète")
	}

	return retenues, ecartees, nil
}

// refusDeLaLigneDuLot nomme ce qu'on reproche à une ligne, ou rend "" si elle
// est retenue.
func refusDeLaLigneDuLot(ligne string) string {
	adresse, err := url.Parse(ligne)
	if err != nil {
		return "adresse illisible"
	}

	switch {
	case adresse.Scheme == "":
		return "adresse relative : une URL complète est attendue"
	case adresse.Scheme != "http" && adresse.Scheme != "https":
		// Le schéma est recopié dans le motif : « ftp » et « file » n'ont pas
		// le même remède, et un message unique les confondrait.
		return "schéma « " + adresse.Scheme + " » : seuls http et https sont acceptés"
	case adresse.Host == "":
		return "adresse sans nom de domaine"
	}

	return ""
}

// creeLeLot écrit la fournée : son tag, l'enregistrement imports, et une ligne
// import_urls par URL retenue.
//
// Tout dans une transaction : un lot à moitié écrit serait repris par l'ouvrier
// comme s'il était complet, et les URLs manquantes ne reviendraient jamais.
//
// L'instant est un paramètre, et non time.Now() lu ici : c'est lui qui nomme le
// tag, et un test du cas « deux lots dans la même minute » ne peut pas attendre
// que le hasard le produise.
func creeLeLot(app core.App, compte *core.Record, urls []string, instant time.Time) (*core.Record, *core.Record, error) {
	var lot, tag *core.Record

	err := app.RunInTransaction(func(txApp core.App) error {
		var err error
		if tag, err = tagDeLaFournee(txApp, instant); err != nil {
			return err
		}

		lots, err := txApp.FindCollectionByNameOrId("imports")
		if err != nil {
			return fmt.Errorf("collection imports : %w", err)
		}

		lot = core.NewRecord(lots)
		// L'auteur vient de la session, jamais du formulaire : un champ posté
		// serait falsifiable. Les hooks de requête ne couvrent pas ce chemin.
		poseLAuteur(lot, compte)
		lot.Set("tag", tag.Id)
		lot.Set("status", "en_cours")
		if err := txApp.Save(lot); err != nil {
			return fmt.Errorf("création du lot : %w", err)
		}

		lignes, err := txApp.FindCollectionByNameOrId("import_urls")
		if err != nil {
			return fmt.Errorf("collection import_urls : %w", err)
		}
		for rang, adresse := range urls {
			ligne := core.NewRecord(lignes)
			ligne.Set("batch", lot.Id)
			ligne.Set("url", adresse)
			// Les rangs commencent à 1, comme les ingrédients : à 0, la
			// première ligne ne se distinguerait pas d'un rang jamais posé.
			ligne.Set("position", rang+1)
			ligne.Set("status", "a_faire")
			if err := txApp.Save(ligne); err != nil {
				return fmt.Errorf("enregistrement de %q : %w", adresse, err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	return lot, tag, nil
}

// tagDeLaFournee crée le tag sous lequel les recettes du lot se retrouveront.
//
// Par le nom, et jamais par le slug : normaliseLeTag (tags.go) recalcule
// toujours le slug depuis le nom, délibérément — un slug posé par l'appelant
// serait écrasé, et le poser ici contournerait l'unicité. Le discriminant se
// met donc dans le nom, et le hook en tire le slug.
func tagDeLaFournee(txApp core.App, instant time.Time) (*core.Record, error) {
	collection, err := txApp.FindCollectionByNameOrId("tags")
	if err != nil {
		return nil, fmt.Errorf("collection tags : %w", err)
	}

	// La minute, et non la seconde : c'est un nom que quelqu'un doit
	// reconnaître dans une liste de tags, pas un identifiant.
	base := "Import du " + instant.Format("02/01/2006") + " à " + instant.Format("15h04")

	for rang := 1; rang <= fourneesMaxParMinute; rang++ {
		nom := base
		if rang > 1 {
			nom += " (" + strconv.Itoa(rang) + ")"
		}

		// Le slug est calculé comme le hook le fera, et non deviné : deux
		// recettes du calcul divergeraient au premier cas particulier.
		libre, err := leSlugEstLibre(txApp, texte.Slug(nomNormalise(nom)))
		if err != nil {
			return nil, err
		}
		if !libre {
			continue
		}

		tag := core.NewRecord(collection)
		tag.Set("name", nom)
		if err := txApp.Save(tag); err != nil {
			return nil, fmt.Errorf("création du tag %q : %w", nom, err)
		}
		return tag, nil
	}

	return nil, fmt.Errorf("%w : %d lots portent déjà le nom %q", erreurFourneesEpuisees, fourneesMaxParMinute, base)
}

// leSlugEstLibre dit si aucun tag ne porte déjà ce slug.
//
// La valeur passe par params, comme dans tagsDepuisSaisie : elle vient d'une
// horloge ici, mais un filtre construit par concaténation est une habitude
// qu'on ne prend pas à moitié.
func leSlugEstLibre(txApp core.App, slug string) (bool, error) {
	_, err := txApp.FindFirstRecordByFilter("tags", "slug = {:slug}", dbx.Params{"slug": slug})
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, sql.ErrNoRows):
		return true, nil
	default:
		return false, fmt.Errorf("recherche du tag %q : %w", slug, err)
	}
}
