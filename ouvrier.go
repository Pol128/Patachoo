package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/Pol128/Patachoo/jsonld"
	"github.com/Pol128/Patachoo/recuperation"
)

// L'ouvrier de l'import en lot : il consomme la file que la page de saisie
// écrit, une URL à la fois, et n'en sort que ce que PATA-8 et PATA-7 lui
// rendent.
//
// Un seul pour l'instance, et une seule file. Deux lots menés de front sur le
// même domaine y feraient deux requêtes par seconde, et le cadencement par
// domaine ne voudrait plus rien dire s'il était par lot.
//
// C'est le seul endroit du produit qui sorte sur le réseau en rafale : la
// politesse de PATA-8 — robots.txt, plafonds de taille et de temps — s'y tient
// ou s'y perd, et le Crawl-delay s'y ajoute.

// Les statuts que la migration de la sous-tâche 1 a posés. Ils sont écrits ici
// une fois : une chaîne recopiée au fil du code finit par diverger d'une
// lettre, et le select de PocketBase le refuserait à l'écriture, pas à la
// lecture.
const (
	statutAFaire       = "a_faire"
	statutEnCours      = "en_cours"
	statutImportee     = "importee"
	statutDejaPresente = "deja_presente"
	statutEchec        = "echec"
	statutTermine      = "termine"
)

// causeEnregistrement est la onzième cause : ni un refus du site, ni un défaut
// de son balisage, mais une panne de notre côté — une lecture ou une écriture
// que la base refuse.
//
// Elle existe parce qu'une ligne doit toujours finir par un sort définitif :
// laissée en cours, la reprise la rejouerait à chaque démarrage, indéfiniment.
const causeEnregistrement = "enregistrement"

// sondageDeLaFile est l'attente entre deux passages sur la file quand il n'y a
// rien à faire. C'est aussi ce qu'un lot tout juste créé attend, au pire, avant
// que sa première URL parte.
const sondageDeLaFile = time.Second

// filesMax borne le nombre d'hôtes menés de front, et donc le nombre de
// requêtes sortantes en vol : une file est séquentielle.
//
// Une fournée de 500 URLs peut viser autant de domaines distincts, et autant de
// requêtes sortantes simultanées ne seraient ni polies, ni tenables pour une
// petite instance. Les hôtes se suivent donc par vagues ; à l'intérieur d'une
// vague, ils progressent ensemble.
//
// Ce que le découpage coûte, et qui est assumé : une vague attend son hôte le
// plus lent avant que la suivante démarre. Au-delà de filesMax hôtes, la durée
// totale d'un lot n'est donc plus celle du plus gros hôte seul. Un sémaphore de
// filesMax jetons l'éviterait ; il ferait démarrer une file au moment où une
// autre se libère, c'est-à-dire à un instant que l'horloge des tests ne saurait
// pas rendre reproductible.
const filesMax = 8

// ouvrier tient l'horloge et la cadence pour toute la durée du service.
type ouvrier struct {
	app     core.App
	horloge horlogeDuLot
	cadence *cadence
	// creation sérialise le second bout de la déduplication. Voir
	// enregistreSiInedite : c'est le seul endroit où deux files se croisent.
	creation sync.Mutex
}

func nouvelOuvrier(app core.App, h horlogeDuLot) *ouvrier {
	return &ouvrier{app: app, horloge: h, cadence: nouvelleCadence(h)}
}

// brancheLOuvrier démarre l'ouvrier avec le serveur et l'arrête avec lui.
//
// L'arrêt attend que l'ouvrier ait rendu la main : ce sont ses dernières
// écritures — la ligne en cours qui repasse à faire — qui font que la reprise
// du démarrage suivant retrouve un état cohérent, et elles ont besoin d'une
// base encore ouverte.
func brancheLOuvrier(app core.App) {
	o := nouvelOuvrier(app, horlogeSysteme{})
	ctx, arrete := context.WithCancel(context.Background())
	fini := make(chan struct{})
	// Un booléen nu ne suffit pas : OnServe est déclenché par la commande
	// serve, OnTerminate par le traitement du signal, et rien ne les ordonne.
	// La goroutine d'arrêt y lirait faux, sauterait l'attente, et les dernières
	// écritures de l'ouvrier se feraient sur une base en train de se fermer.
	var demarre atomic.Bool

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		demarre.Store(true)
		go func() {
			defer close(fini)

			o.tourne(ctx)
		}()
		return se.Next()
	})

	app.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
		arrete()
		// Une sous-commande — une fusion, une migration — termine sans jamais
		// avoir servi : il n'y a alors personne à attendre.
		if demarre.Load() {
			<-fini
		}
		return e.Next()
	})
}

// tourne consomme la file tant que le contexte vit.
func (o *ouvrier) tourne(ctx context.Context) {
	if err := o.reprend(ctx); err != nil && ctx.Err() == nil {
		o.app.Logger().Error("reprise des lots en cours", "erreur", err)
	}

	for ctx.Err() == nil {
		if err := o.horloge.Attends(ctx, sondageDeLaFile); err != nil {
			return
		}
		if err := o.traiteLesLotsEnCours(ctx); err != nil && ctx.Err() == nil {
			o.app.Logger().Error("traitement de la file des lots", "erreur", err)
		}
	}
}

// reprend est ce que l'ouvrier fait au démarrage : rendre à la file les lignes
// qu'un arrêt a laissées en cours, puis traiter les lots ouverts.
func (o *ouvrier) reprend(ctx context.Context) error {
	if err := o.rendLesLignesInterrompues(); err != nil {
		return err
	}
	return o.traiteLesLotsEnCours(ctx)
}

// rendLesLignesInterrompues remet à faire les lignes qu'un processus arrêté au
// mauvais moment a laissées en cours.
//
// Sans ce geste, elles ne seraient plus réclamées par personne : l'ouvrier ne
// prend que ce qui est à faire, et le lot n'atteindrait jamais son terme.
func (o *ouvrier) rendLesLignesInterrompues() error {
	lignes, err := o.app.FindAllRecords("import_urls", dbx.HashExp{"status": statutEnCours})
	if err != nil {
		return fmt.Errorf("lecture des lignes interrompues : %w", err)
	}

	for _, ligne := range lignes {
		ligne.Set("status", statutAFaire)
		if err := o.app.Save(ligne); err != nil {
			return fmt.Errorf("reprise de la ligne %s : %w", ligne.Id, err)
		}
	}
	return nil
}

// traiteLesLotsEnCours mène les lots ouverts, dans l'ordre de leur création :
// une fournée commencée finit avant que la suivante ne démarre.
func (o *ouvrier) traiteLesLotsEnCours(ctx context.Context) error {
	lots, err := o.app.FindRecordsByFilter("imports", "status = {:statut}", "created", 0, 0,
		dbx.Params{"statut": statutEnCours})
	if err != nil {
		return fmt.Errorf("lecture des lots en cours : %w", err)
	}

	for _, lot := range lots {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := o.traiteLeLot(ctx, lot); err != nil {
			return err
		}
	}
	return nil
}

// traiteLeLot mène les lignes restantes d'un lot, une file par hôte.
func (o *ouvrier) traiteLeLot(ctx context.Context, lot *core.Record) error {
	lignes, err := o.app.FindRecordsByFilter("import_urls",
		"batch = {:lot} && status = {:statut}", "position", 0, 0,
		dbx.Params{"lot": lot.Id, "statut": statutAFaire})
	if err != nil {
		return fmt.Errorf("lecture des lignes du lot %s : %w", lot.Id, err)
	}

	files := parHote(lignes)
	for debut := 0; debut < len(files); debut += filesMax {
		vague := files[debut:min(debut+filesMax, len(files))]

		taches := make([]func(), 0, len(vague))
		for _, file := range vague {
			taches = append(taches, func() { o.traiteLaFile(ctx, lot, file) })
		}
		enFiles(o.horloge, taches)

		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	return o.clotSiTermine(lot)
}

// parHote range les lignes en une file par hôte : chaque file dans l'ordre de
// position, les files dans l'ordre d'apparition de leur première ligne.
//
// L'ordre importe : c'est la liste que l'utilisateur a collée, et le rapport de
// la sous-tâche 4 la relira dans le même sens.
func parHote(lignes []*core.Record) [][]*core.Record {
	rang := map[string]int{}
	var files [][]*core.Record

	for _, ligne := range lignes {
		hote := hoteDe(ligne.GetString("url"))
		if _, vu := rang[hote]; !vu {
			rang[hote] = len(files)
			files = append(files, nil)
		}
		files[rang[hote]] = append(files[rang[hote]], ligne)
	}
	return files
}

// hoteDe rend l'hôte d'une adresse, en minuscules : c'est la clé du
// cadencement, et Example.com est le même site qu'example.com.
func hoteDe(adresse string) string {
	cible, err := url.Parse(adresse)
	if err != nil {
		// La validation de la saisie a déjà écarté ce cas ; s'il revenait,
		// l'adresse entière fait une clé qui ne se confond avec aucune autre.
		return adresse
	}
	return strings.ToLower(cible.Hostname())
}

// traiteLaFile mène les lignes d'un même hôte, l'une après l'autre.
func (o *ouvrier) traiteLaFile(ctx context.Context, lot *core.Record, lignes []*core.Record) {
	for _, ligne := range lignes {
		if ctx.Err() != nil {
			return
		}
		if err := o.traiteLaLigne(ctx, lot, ligne); err != nil {
			if ctx.Err() != nil {
				return
			}
			// L'échec d'une URL n'arrête pas la fournée : c'est le cas
			// ordinaire, et le rapport de fin le dira ligne à ligne.
			o.app.Logger().Error("import en lot", "url", ligne.GetString("url"), "erreur", err)
		}
	}
}

// traiteLaLigne mène une URL de bout en bout : PATA-8, puis PATA-7, puis la
// recette.
func (o *ouvrier) traiteLaLigne(ctx context.Context, lot, ligne *core.Record) error {
	adresse := ligne.GetString("url")
	if err := o.poseLeStatut(ligne, statutEnCours); err != nil {
		return err
	}

	// Déduplication, premier bout : sur l'adresse soumise, et avant tout
	// appel. Une URL déjà connue ne mérite pas qu'on dérange le site.
	connue, err := laSourceEstConnue(o.app, adresse)
	if err != nil {
		return o.poseLaPanne(ligne, err)
	}
	if connue {
		return o.poseLeStatut(ligne, statutDejaPresente)
	}

	// La cadence porte sur les appels à recuperePage, et un appel émet deux
	// requêtes : recuperation demande le robots.txt de l'hôte, puis la page,
	// sans les mettre en cache. Une fournée de N URLs sur un même hôte lui en
	// envoie donc 2N, par paires collées — la promesse « au plus une par
	// seconde » ne vaut, ici, que pour la couture que nous tenons.
	//
	// Écart assumé et sorti de cette tâche : le corriger demande de faire
	// attendre son tour à chaque requête depuis recuperation, ce que PATA-42
	// met hors périmètre, et met en jeu la borne de temps que l'import
	// unitaire de PATA-9 doit à son utilisateur. C'est PATA-47.
	hote := hoteDe(adresse)
	if err := o.cadence.attendSonTour(ctx, hote); err != nil {
		return o.rendLaLigne(ligne, err)
	}

	page, err := recuperePage(ctx, adresse)
	if err != nil {
		if ctx.Err() != nil {
			// Le serveur s'arrête : la ligne n'a pas échoué, elle n'a pas eu
			// lieu. Elle repart à faire, et le lot reste en cours.
			return o.rendLaLigne(ligne, ctx.Err())
		}
		return o.poseLEchec(ligne, err)
	}
	// Le Crawl-delay ne se lit qu'une fois la réponse obtenue : il s'applique
	// donc à partir de la requête suivante, vers ce domaine seulement.
	o.cadence.retiens(hote, page.DelaiAnnonce)

	return o.enregistreSiInedite(lot, ligne, page)
}

// enregistreSiInedite est la déduplication par le second bout, et l'écriture
// qu'elle autorise : deux adresses distinctes peuvent rediriger vers la même
// page, et c'est la finale qui désigne la recette.
//
// La lecture et l'écriture tiennent ensemble, sous un verrou que toutes les
// files partagent. Sans lui, deux files menées de front — www.site.fr et
// site.fr sont deux hôtes, donc deux files — lisent l'une après l'autre que la
// page est inconnue, puis créent chacune leur recette. Rien ne les rattraperait
// en base : recipes.source_url ne porte pas d'index unique, et lui en poser un
// concernerait toutes les recettes du produit, pas la fournée.
//
// Le verrou ne couvre ni requête ni attente — il est pris une fois la page
// obtenue et rendu à la fin de l'écriture. Ce que les files ont à mener de
// front, c'est le réseau ; elles continuent de le faire.
//
// L'extraction du balisage reste dedans, et c'est un choix : l'en sortir
// obligerait à la faire avant de savoir si la page est déjà connue, et un
// balisage cassé sur une page déjà importée passerait alors de « déjà
// présente » à un échec. Ce qu'elle coûte est un calcul sur un corps déjà en
// mémoire, borné par la taille maximale de PATA-8 — pas une attente.
func (o *ouvrier) enregistreSiInedite(lot, ligne *core.Record, page recuperation.Page) error {
	o.creation.Lock()
	defer o.creation.Unlock()

	connue, err := laSourceEstConnue(o.app, page.URLFinale)
	if err != nil {
		return o.poseLaPanne(ligne, err)
	}
	if connue {
		return o.poseLeStatut(ligne, statutDejaPresente)
	}

	trouvee, err := jsonld.Extraire(page.Corps)
	if err != nil {
		return o.poseLEchec(ligne, err)
	}

	return o.enregistre(lot, ligne, page, trouvee)
}

// enregistre écrit la recette et le sort de la ligne, en une transaction par
// URL : une recette créée dont la ligne resterait en cours serait réimportée au
// démarrage suivant.
func (o *ouvrier) enregistre(lot, ligne *core.Record, page recuperation.Page, trouvee jsonld.Recette) error {
	err := o.app.RunInTransaction(func(txApp core.App) error {
		collection, err := txApp.FindCollectionByNameOrId("recipes")
		if err != nil {
			return fmt.Errorf("collection recipes : %w", err)
		}

		recette := core.NewRecord(collection)
		if err := creeLaRecetteImportee(txApp, recette, lot, page, trouvee); err != nil {
			return err
		}

		ligne.Set("status", statutImportee)
		ligne.Set("cause", "")
		ligne.Set("code", 0)
		ligne.Set("recipe", recette.Id)
		return txApp.Save(ligne)
	})
	if err == nil {
		return nil
	}

	// L'écriture a échoué et la transaction est défaite ; la ligne, elle, garde
	// en mémoire ce qu'on lui avait posé. poseLaPanne la remet à plat.
	return o.poseLaPanne(ligne, err)
}

// creeLaRecetteImportee écrit la recette que la page publie.
//
// La correspondance des champs est celle de PATA-9 — preRemplissageDe, écrite
// une fois et appelée des deux côtés : l'import unitaire la rend dans un
// formulaire, le lot l'enregistre. Ce que le lot ajoute est ici : l'auteur du
// lot, la source suivie, et le tag de la fournée.
func creeLaRecetteImportee(txApp core.App, recette, lot *core.Record, page recuperation.Page, trouvee jsonld.Recette) error {
	champs := preRemplissageDe(trouvee)

	recette.Set("title", strings.TrimSpace(champs.Titre))
	recette.Set("servings", champs.Portions)
	recette.Set("prep_time", champs.TempsPreparation)
	recette.Set("cook_time", champs.TempsCuisson)
	recette.Set("instructions", champs.Instructions)
	// L'URL finale, celle du dernier saut, et non celle qui a été soumise :
	// c'est elle qui désigne la recette, et c'est sur elle que porte la
	// déduplication.
	recette.Set("source_url", page.URLFinale)
	recette.Set("source_name", nomDuSite(litOpenGraph(page.Corps).NomDuSite, page.URLFinale))
	// L'image n'est pas téléchargée : c'est PATA-10, et le lot s'en tient à
	// l'unitaire d'avant elle — une recette importée en fournée s'illustre
	// depuis le formulaire d'édition.

	// Une recette créée par notre propre code passe par app.Save() et ne
	// déclenche aucun hook de requête : attribueALAppelant ne la voit pas, et
	// poseLAuteur est écrite pour cet appel-là.
	//
	// Un auteur introuvable — lot lancé depuis un compte superuser, dont
	// l'identifiant ne désigne aucun users ; compte supprimé pendant que son
	// lot attendait — arrête cette ligne au lieu de créer une recette sans
	// auteur. La règle de suppression de recipes exige
	// created_by = @request.auth.id : une recette sans auteur ne serait
	// supprimable par aucune session, et rien dans le rapport de la fournée ne
	// dirait pourquoi. La ligne prend un échec, et le dit.
	titulaire, err := txApp.FindRecordById("users", lot.GetString(champAuteur))
	if err != nil {
		return fmt.Errorf("auteur du lot %s : %w", lot.Id, err)
	}
	poseLAuteur(recette, titulaire)

	ajouteLeTag(recette, lot.GetString("tag"))

	if err := txApp.Save(recette); err != nil {
		return fmt.Errorf("création de la recette %q : %w", champs.Titre, err)
	}

	// raw seul : le hook de PATA-6 remplit les cinq champs dérivés à la
	// création. Le découpage des lignes est celui du formulaire, et il n'en
	// existe qu'un.
	lignes := formulaireRecette{Ingredients: champs.Ingredients}.lignesDIngredients()
	return remplaceLesIngredients(txApp, recette, lignes)
}

// ajouteLeTag ajoute le tag de la fournée à ceux que la recette porte déjà.
//
// Un ajout, et non un Set d'une liste d'un seul élément : aujourd'hui une
// recette importée n'arrive avec aucun tag et le cas ne se produit pas, mais le
// jour où la correspondance des champs en produira, ce détail sera la
// différence entre garder et perdre.
func ajouteLeTag(recette *core.Record, tag string) {
	if tag == "" {
		return
	}

	poses := recette.GetStringSlice("tags")
	if slices.Contains(poses, tag) {
		return
	}
	recette.Set("tags", append(poses, tag))
}

// laSourceEstConnue dit si une recette porte déjà cette adresse en source.
//
// La valeur passe par params : elle vient d'un site tiers, et un filtre
// construit par concaténation est une habitude qu'on ne prend pas à moitié.
func laSourceEstConnue(app core.App, adresse string) (bool, error) {
	if adresse == "" {
		return false, nil
	}

	_, err := app.FindFirstRecordByFilter("recipes", "source_url = {:url}", dbx.Params{"url": adresse})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("recherche de la source %q : %w", adresse, err)
	}
}

// clotSiTermine passe le lot en terminé quand plus aucune de ses lignes
// n'attend son tour, quel que soit le sort de chacune.
func (o *ouvrier) clotSiTermine(lot *core.Record) error {
	restantes, err := o.app.CountRecords("import_urls",
		dbx.HashExp{"batch": lot.Id},
		dbx.In("status", statutAFaire, statutEnCours))
	if err != nil {
		return fmt.Errorf("décompte des lignes du lot %s : %w", lot.Id, err)
	}
	if restantes > 0 {
		return nil
	}

	lot.Set("status", statutTermine)
	if err := o.app.Save(lot); err != nil {
		return fmt.Errorf("clôture du lot %s : %w", lot.Id, err)
	}
	return nil
}

// --- Le sort d'une ligne ----------------------------------------------------

// poseLeStatut écrit un sort sans cause : en cours, ou déjà présente.
func (o *ouvrier) poseLeStatut(ligne *core.Record, statut string) error {
	ligne.Set("status", statut)
	if err := o.app.Save(ligne); err != nil {
		return fmt.Errorf("statut %q de la ligne %s : %w", statut, ligne.Id, err)
	}
	return nil
}

// rendLaLigne remet la ligne à faire et rend l'erreur qui l'a interrompue.
func (o *ouvrier) rendLaLigne(ligne *core.Record, motif error) error {
	if err := o.poseLeStatut(ligne, statutAFaire); err != nil {
		return err
	}
	return motif
}

// poseLaPanne donne son sort à une ligne qu'une panne de notre côté — une
// lecture ou une écriture que la base refuse — a empêché d'aboutir.
//
// Marquée en échec, et non laissée en cours : rien ne dit qu'une seconde
// tentative ferait mieux, et une ligne sans sort définitif ne serait plus
// réclamée par personne. L'ouvrier ne prend que ce qui est à faire, et les
// lignes interrompues ne sont rendues qu'au démarrage : le lot resterait en
// cours jusqu'au prochain redémarrage du processus.
//
// Le motif est rendu tel quel : c'est la file qui le journalise, une fois.
func (o *ouvrier) poseLaPanne(ligne *core.Record, motif error) error {
	ligne.Set("status", statutEchec)
	ligne.Set("cause", causeEnregistrement)
	ligne.Set("code", 0)
	ligne.Set("recipe", "")
	if err := o.app.Save(ligne); err != nil {
		return fmt.Errorf("marquage de la ligne %s : %w", ligne.Id, err)
	}
	return motif
}

// poseLEchec écrit le sort d'une URL qui n'a pas abouti : la cause nommée, et
// le code HTTP quand l'échec en porte un.
func (o *ouvrier) poseLEchec(ligne *core.Record, motif error) error {
	cause, code := causeDe(motif)

	ligne.Set("status", statutEchec)
	ligne.Set("cause", cause)
	ligne.Set("code", code)
	if err := o.app.Save(ligne); err != nil {
		return fmt.Errorf("échec de la ligne %s : %w", ligne.Id, err)
	}
	return nil
}

// causeDe nomme un échec dans les mots de recuperation ou de jsonld.
//
// Le tri se fait sur les causes nommées, jamais sur le texte de l'erreur : un
// message technique ne doit pas décider de ce qui s'enregistre. Le code HTTP
// se lit par errors.As, et non en cherchant trois chiffres dans une chaîne.
func causeDe(err error) (string, int) {
	var refus *recuperation.Erreur
	if errors.As(err, &refus) {
		return refus.Cause, refus.Code
	}

	switch {
	case errors.Is(err, jsonld.ErrSansRecette):
		return jsonld.SansRecette, 0
	case errors.Is(err, jsonld.ErrAucunBalisage):
		return jsonld.AucunBalisage, 0
	case errors.Is(err, jsonld.ErrJSONInvalide):
		return jsonld.JSONInvalide, 0
	case errors.Is(err, jsonld.ErrTitreAbsent):
		return jsonld.TitreAbsent, 0
	}

	// Aucune des dix : nous n'en savons pas plus que « la page n'a pas été
	// atteinte », et c'est déjà ce que PATA-9 en dit à l'utilisateur.
	return recuperation.Injoignable, 0
}
