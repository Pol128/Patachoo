package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
)

// L'établi : la page qui lance une analyse de corpus, et qui en montre
// l'avancement.
//
// Ce fichier ne lit ni n'écrit le carnet. Il choisit une source, la remet à
// l'ouvrier (analyse.go), et rend ce que la passe en dit. La promesse de
// l'établi — « il lit le carnet, il ne le modifie pas » — tient donc ici par
// construction : la seule chose que cette page écrive est l'enregistrement de
// la passe, et c'est l'ouvrier qui le fait.
//
// Le corpus fourni n'est stocké nulle part : ni pièce jointe, ni collection de
// lignes sur le patron de la fournée. Il est lu en mémoire le temps de la
// passe, et disparaît avec elle — ce qui a deux conséquences assumées, déjà
// écrites en tête de analyse.go : le plafond de taille est une borne mémoire
// du processus, et une analyse interrompue ne se rejoue pas.

// cheminDeLEtabli porte les deux routes du lancement : le GET rend la page, le
// POST dépose le travail. Une seule constante, pour la même raison que
// cheminDuLot — une URL recopiée finit par diverger de celle qui est branchée.
//
// C'est aussi le préfixe de l'établi : les écrans de résultats (PATA-125 à
// PATA-127) s'accrochent dessous, et n'en inventent pas d'autre.
const cheminDeLEtabli = "/etabli"

// cheminDeLAvancementDeLEtabli porte le fragment que HTMX redemande pendant
// qu'une passe tourne, dans le prolongement du précédent.
//
// Une route à part, et non le même chemin rendu en fragment comme le fait le
// suivi d'une fournée : ici le fragment est un bloc *dans* la page de
// lancement, pas la page elle-même. Rendre le corps entier dans un hx-target
// y remettrait le formulaire à chaque rafraîchissement.
const cheminDeLAvancementDeLEtabli = cheminDeLEtabli + "/avancement"

// Les trois champs du formulaire. Écrits une fois : le gabarit, la route et
// les tests les partagent, et une chaîne recopiée finit par diverger d'une
// lettre — que rien ne signalerait, un champ absent se lisant comme un champ
// vide.
const (
	champSourceDeLEtabli = "source"
	champCorpusColle     = "corpus"
	champCorpusTeleverse = "fichier"
)

// plafondDuCorpsDeLEtabli borne le corps d'un lancement.
//
// Celui de l'analyse, et non un chiffre à lui : un corps accepté ici ne doit
// pas pouvoir être refusé plus loin par l'ouvrier. L'écart tient dans
// l'habillage multipart, quelques centaines d'octets qui font qu'un corpus de
// très exactement 32 Mio est refusé au corps — dans le sens sûr, avec un
// message qui dit le plafond, plutôt qu'accepté puis mis en échec.
//
// Il se pose sur le corps, et non dans le gestionnaire : exigeLeJetonAntiRejeu
// appelle valeursSoumises, donc ParseMultipartForm, et lit le corps entier
// avant que le gestionnaire ne commence. Un contrôle posé plus loin arriverait
// après que tout est lu, et après que ce qui dépasse la mémoire du formulaire
// est parti dans un fichier temporaire.
const plafondDuCorpsDeLEtabli = plafondDOctetsDUneAnalyse

// prioriteBorneDuCorpsDeLEtabli place la borne de taille avant celle de
// PocketBase, et donc avant tout ce qui lit le corps.
//
// Les middlewares du routeur et ceux de la route sont fondus dans un même
// crochet trié par priorité croissante (tools/router/router.go) : une priorité
// négative passe donc avant exigeLeJetonAntiRejeu, qui n'en déclare pas et est
// le premier à lire le corps.
//
// Mais il ne suffit pas de passer avant lui : apis.NewRouter pose déjà
// BodyLimit(DefaultMaxBodySize) sur le routeur racine, et ce plafond-là vaut
// exactement le nôtre. Son contrôle est optimiste — il compare le
// Content-Length annoncé et rend son erreur *sans passer la main* —, si bien
// qu'une borne posée après lui n'est jamais atteinte dès que le corps annonce
// sa taille, c'est-à-dire dès qu'un navigateur téléverse. Le refus ressortirait
// alors en JSON, ce que le critère d'acceptation exclut.
//
// Un cran avant la sienne, donc, et lu sur sa constante plutôt que recopié :
// le jour où PocketBase déplace la sienne, celle-ci suit. Reste en aval du
// chargement de la session (DefaultLoadAuthTokenMiddlewarePriority, -1020) et
// du plafond de débit (-1000) : e.Auth est posé quand cette borne s'exécute, et
// un corps hors plafond compte comme une requête.
const prioriteBorneDuCorpsDeLEtabli = apis.DefaultBodyLimitMiddlewarePriority - 1

// cadenceDeLAvancementDeLEtabli est l'intervalle du rafraîchissement HTMX.
//
// Deux secondes, comme le suivi d'une fournée : l'ouvrier pousse ses compteurs
// au plus une fois par seconde (cadenceDeLAvancement), donc rafraîchir plus
// vite ne montrerait rien de plus.
const cadenceDeLAvancementDeLEtabli = "every 2s"

// Les quatre refus de la page. Ils disent ce qui n'allait pas et ce qu'il faut
// faire, sans jamais reprendre la saisie dans leur texte : c'est le champ qui
// la reprend, où le gabarit l'échappe.
const (
	messageSourceAbsente = "Choisissez ce qu'il faut analyser : la base de l'instance, ou un corpus fourni."
	messageSourcesMelees = "Une seule source à la fois : la base de l'instance, ou un corpus fourni, mais pas les deux."
	messageCorpusAbsent  = "Le corpus fourni est vide : collez des lignes d'ingrédients, ou joignez un fichier."
	messageInstanceVide  = "La base de l'instance ne porte aucune ligne d'ingrédient : il n'y a rien à analyser."
)

// messageDuCorpsTropGros dit le plafond en toutes lettres.
//
// La valeur est lue sur la constante et jamais recopiée, comme refusDuChamp le
// fait déjà des bornes du schéma : un message qui répète « 32 Mio » ment le
// jour où la constante bouge.
func messageDuCorpsTropGros() string {
	return fmt.Sprintf(
		"Le corpus dépasse la taille acceptée : %d Mio au plus. Il n'a pas été analysé.",
		plafondDuCorpsDeLEtabli>>20)
}

// donneesEtabli est ce que le gabarit de la page de lancement reçoit.
type donneesEtabli struct {
	donneesPage

	// PlafondEnMio est le plafond annoncé avant l'envoi. Calculé ici et non
	// écrit dans le gabarit : c'est ce qui fait qu'un chiffre changé dans le
	// code change la page.
	PlafondEnMio int

	// Corpus est la saisie telle qu'elle a été collée : un corpus refusé ne
	// doit pas se retaper. Vide sur la page nue, et vide aussi après un refus
	// de taille — là, justement, la saisie n'a pas été lue.
	Corpus string

	// Avancement est le travail à montrer, ou nil quand l'instance n'a encore
	// rien analysé : le gabarit s'ouvre alors sur un {{with}} et n'écrit pas
	// de bloc vide.
	Avancement *donneesAvancement
}

// donneesAvancement est ce que le fragment de suivi reçoit, dans les trois
// états d'une passe.
//
// Il embarque donneesPage parce qu'il se rend aussi seul, sous sa propre
// route : sans JavaScript, le bloc reste atteignable comme page.
type donneesAvancement struct {
	donneesPage

	// Lien est l'adresse à laquelle le fragment se redemande, et Cadence
	// l'intervalle du hx-trigger : le gabarit ne les recompose pas.
	Lien    string
	Cadence string

	// EnCours commande le rafraîchissement : une passe close ne se redemande
	// plus, et l'onglet oublié cesse d'interroger le serveur.
	EnCours bool

	// Statut et Source sont les libellés français. Jamais la valeur
	// enregistrée : « en_cours » et « fourni » sont des clés de schéma, pas des
	// mots qu'on montre.
	Statut string
	Source string

	Lignes int
	Formes int
	Date   string
}

// brancheLEtabli pose les trois routes de l'établi.
//
// Toutes derrière exigeUnCurateur : l'établi est le premier écran réservé du
// produit, et le droit se contrôle avant la lecture de la saisie comme avant
// celle de la base. Le POST porte en plus la borne de taille et le contrôle
// anti-rejeu, dans cet ordre — la première borne ce que le second lit.
//
// L'anti-rejeu avant le droit, comme lot.go le fait avant la session : une
// requête forgée par un autre site n'a pas à être distinguée selon que celui
// qui la subit est curateur ou non.
func brancheLEtabli(routeur *router.Router[*core.RequestEvent], ouvrier *ouvrierDAnalyse) {
	routeur.GET(cheminDeLEtabli, pageDeLEtabli).Bind(exigeUnCurateur())
	routeur.POST(cheminDeLEtabli, lanceUnePasse(ouvrier)).Bind(
		borneLeCorpsDeLEtabli(), exigeLeJetonAntiRejeu(), exigeUnCurateur())
	routeur.GET(cheminDeLAvancementDeLEtabli, avancementDeLEtabli).Bind(exigeUnCurateur())
}

// borneLeCorpsDeLEtabli refuse un corps plus gros que le plafond, et rend ce
// refus en page.
//
// Le rattrapage est ici plutôt que dans le gestionnaire pour la raison écrite
// sur plafondDuCorpsDeLEtabli : le corps est lu avant lui. Le commentaire de
// exigeLeJetonAntiRejeu prévoit ce cas et laisse remonter l'erreur — « un corps
// illisible n'est pas un refus de jeton » —, c'est donc à cette couche de la
// reconnaître et de dire le plafond.
//
// Sur cette route seule, et non sur le routeur : les autres formulaires du
// produit ont leurs propres bornes, et l'API REST parle JSON à ses clients.
func borneLeCorpsDeLEtabli() *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "patachooBorneLeCorpsDeLEtabli",
		Priority: prioriteBorneDuCorpsDeLEtabli,
		Func: func(e *core.RequestEvent) error {
			// Le contrôle optimiste, dans les mêmes termes que celui de
			// PocketBase et juste avant lui : c'est le chemin du navigateur,
			// qui annonce toujours la taille de ce qu'il envoie. Rendu ici,
			// le refus est une page ; laissé à BodyLimit, c'est du JSON.
			if e.Request.ContentLength > plafondDuCorpsDeLEtabli {
				return refuseLeCorpsTropGros(e)
			}

			// Et la borne sur la lecture, pour le corps qui n'annonce rien —
			// un envoi en Transfer-Encoding: chunked, que le contrôle
			// ci-dessus ne peut pas voir venir.
			e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, plafondDuCorpsDeLEtabli)

			err := e.Next()
			var trop *http.MaxBytesError
			if !errors.As(err, &trop) {
				return err
			}
			return refuseLeCorpsTropGros(e)
		},
	}
}

// refuseLeCorpsTropGros rend le refus de taille en page — après avoir
// recontrôlé le droit.
//
// Le recontrôle n'est pas une ceinture de plus : la page rendue ici est la page
// réservée, formulaire de lancement et dernière passe compris, et elle se rend
// hors de la garde. exigeUnCurateur porte la priorité par défaut, donc passe
// après cette borne, et le chemin de l'erreur court-circuite la suite de la
// chaîne — sans ce contrôle, il suffirait de poster plus que le plafond, sans
// aucune session, pour lire l'établi.
//
// Le refus n'est pas habillé en page, lui : c'est celui de exigeUnCurateur,
// mot pour mot, pour qu'un client hors plafond ne se distingue pas d'un autre.
func refuseLeCorpsTropGros(e *core.RequestEvent) error {
	if refuse, err := refuseQuiNEstPasCurateur(e); refuse {
		return err
	}

	// Sans reprendre la saisie : le corps est justement ce qu'on a refusé de
	// lire, et le relire ici serait lever la borne qu'on vient de poser.
	return rendLEtabliAvecStatut(e, http.StatusRequestEntityTooLarge, "", messageDuCorpsTropGros())
}

// pageDeLEtabli rend le formulaire nu, et l'avancement s'il y a un travail.
func pageDeLEtabli(e *core.RequestEvent) error {
	return rendLEtabli(e, "", "")
}

// rendLEtabli rend la page de lancement, avec un message s'il y en a un et la
// saisie telle qu'elle a été collée.
func rendLEtabli(e *core.RequestEvent, corpus, message string) error {
	return rendLEtabliAvecStatut(e, http.StatusOK, corpus, message)
}

// rendLEtabliAvecStatut rend la même page sous un autre code de retour.
//
// Le dépassement du plafond en a besoin : un refus rendu 200 ferait passer pour
// une page valide ce que le protocole doit signaler comme un refus.
func rendLEtabliAvecStatut(e *core.RequestEvent, statut int, corpus, message string) error {
	avancement, err := dernierAvancement(e.App)
	if err != nil {
		return err
	}

	return rendreAvecStatut(e, statut, "etabli.html", "etabli-corps.html", &donneesEtabli{
		donneesPage:  donneesPage{Titre: "L'établi — Patachoo", Message: message},
		PlafondEnMio: plafondDuCorpsDeLEtabli >> 20,
		Corpus:       corpus,
		Avancement:   avancement,
	}, "etabli-avancement-corps.html")
}

// avancementDeLEtabli rend le bloc de suivi — fragment sous HTMX, page
// entière sinon, c'est rendre qui tranche.
func avancementDeLEtabli(e *core.RequestEvent) error {
	avancement, err := dernierAvancement(e.App)
	if err != nil {
		return err
	}
	// Aucune passe : le bloc se rend tout de même, vide. Un 404 ferait échouer
	// le rafraîchissement d'une page ouverte avant le premier lancement.
	if avancement == nil {
		avancement = &donneesAvancement{Lien: cheminDeLAvancementDeLEtabli}
	}
	avancement.Titre = "L'établi — Patachoo"

	return rendre(e, "etabli-avancement.html", "etabli-avancement-corps.html", avancement)
}

// dernierAvancement rend le travail le plus récent, mis en forme, ou nil quand
// l'instance n'en a encore mené aucun.
//
// Le dernier créé, et non le dernier en cours : une passe close reste
// affichée, c'est ce qui fait qu'un lancement suivi d'une redirection montre
// son résultat plutôt qu'une page vide.
func dernierAvancement(app core.App) (*donneesAvancement, error) {
	// Un filtre qui retient tout : FindRecordsByFilter en exige un, et le
	// statut est obligatoire sur chaque passe.
	passes, err := app.FindRecordsByFilter("analyses", "status != ''", "-created", 1, 0)
	if err != nil {
		return nil, fmt.Errorf("dernière analyse : %w", err)
	}
	if len(passes) == 0 {
		return nil, nil
	}
	return avancementDe(passes[0]), nil
}

// avancementDe met une passe en forme pour le gabarit.
func avancementDe(passe *core.Record) *donneesAvancement {
	statut := passe.GetString("status")
	return &donneesAvancement{
		Lien:    cheminDeLAvancementDeLEtabli,
		Cadence: cadenceDeLAvancementDeLEtabli,
		EnCours: statut == statutEnCours,
		Statut:  statutAffiche(statut),
		Source:  sourceAffichee(passe.GetString("source")),
		Lignes:  passe.GetInt("lines"),
		Formes:  passe.GetInt("forms"),
		Date:    dateEnFrancais(passe.GetDateTime("created")),
	}
}

// statutAffiche dit, en français, où en est une passe. Les trois états de
// analyses.status, et rien d'autre.
func statutAffiche(statut string) string {
	switch statut {
	case statutTermine:
		return "terminée"
	case statutEchec:
		return "échouée"
	default:
		return "en cours"
	}
}

// sourceAffichee dit d'où venaient les lignes.
func sourceAffichee(source string) string {
	if source == sourceInstance {
		return "la base de l'instance"
	}
	return "un corpus fourni"
}

// lanceUnePasse valide le formulaire, puis dépose le travail.
//
// La validation d'abord, l'écriture ensuite : un lancement refusé ne doit
// laisser aucune analyse derrière lui, fût-elle vide — c'est ce qui ferait
// mentir le compteur de passes et bloquerait le lancement suivant, l'ouvrier
// n'en menant qu'une à la fois.
func lanceUnePasse(ouvrier *ouvrierDAnalyse) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		source := e.Request.PostFormValue(champSourceDeLEtabli)
		colle := e.Request.PostFormValue(champCorpusColle)

		// Le fichier est ouvert avant toute décision : sa seule présence entre
		// dans le compte des sources, et c'est elle qui distingue le cas mixte
		// d'un simple copier-coller.
		//
		// Deux absences et non une : ErrMissingFile dit qu'un envoi multipart
		// ne porte pas ce champ, ErrNotMultipart que le formulaire a été posté
		// urlencodé — ce que fait un navigateur quand aucun fichier n'est
		// choisi, et ce que fait tout client qui se contente de coller. Les
		// deux sont des absences de fichier, pas des pannes.
		fichier, _, err := e.Request.FormFile(champCorpusTeleverse)
		if err != nil && !errors.Is(err, http.ErrMissingFile) && !errors.Is(err, http.ErrNotMultipart) {
			return err
		}
		if fichier != nil {
			defer fichier.Close()
		}

		if refus := refusDuLancement(source, colle, fichier != nil); refus != "" {
			return rendLEtabli(e, colle, refus)
		}

		if source == sourceInstance {
			return lanceSurLInstance(e, ouvrier)
		}
		return lanceSurLeCorpusFourni(e, ouvrier, colle, fichier)
	}
}

// refusDuLancement nomme ce qu'on reproche au formulaire, ou rend "" s'il est
// recevable.
//
// Les deux sources sont exclusives, et le refus est explicite : préférer l'une
// en silence ferait analyser autre chose que ce que le formulaire montrait.
func refusDuLancement(source, colle string, avecFichier bool) string {
	fourni := strings.TrimSpace(colle) != "" || avecFichier

	switch source {
	case sourceInstance:
		if fourni {
			return messageSourcesMelees
		}
		return ""
	case sourceFournie:
		switch {
		case strings.TrimSpace(colle) != "" && avecFichier:
			return messageSourcesMelees
		case !fourni:
			return messageCorpusAbsent
		}
		return ""
	default:
		return messageSourceAbsente
	}
}

// lanceSurLInstance dépose une passe sur la collection ingredients.
//
// Le refus d'une base vide se prononce avant la mise en file : une passe
// déposée sur zéro ligne se clôrait aussitôt, mais elle laisserait dans la
// liste un travail dont personne ne saurait dire s'il a échoué ou s'il n'avait
// rien à faire.
func lanceSurLInstance(e *core.RequestEvent, ouvrier *ouvrierDAnalyse) error {
	lignes, err := e.App.CountRecords("ingredients")
	if err != nil {
		return fmt.Errorf("décompte des lignes de l'instance : %w", err)
	}
	if lignes == 0 {
		return rendLEtabli(e, "", messageInstanceVide)
	}

	return metEnFileEtRedirige(e, ouvrier, sourceInstance, lignesDeLInstance(e.App), "")
}

// lanceSurLeCorpusFourni dépose une passe sur ce que le formulaire a apporté.
//
// Le corpus est lu en mémoire, et c'est la conséquence directe de « le fichier
// fourni n'est jamais conservé » : le fichier temporaire du téléversement est
// effacé par le serveur dès que la requête est rendue, bien avant que
// l'ouvrier n'ait fini. Lui passer ce fichier reviendrait à lui passer un flux
// qui se ferme sous lui.
//
// La lecture est bornée par la borne du corps, déjà posée en amont : ce qui
// arrive ici tient sous le plafond, sans quoi la requête n'aurait pas atteint
// le gestionnaire.
func lanceSurLeCorpusFourni(e *core.RequestEvent, ouvrier *ouvrierDAnalyse, colle string, fichier io.Reader) error {
	contenu := []byte(colle)
	if fichier != nil {
		lu, err := io.ReadAll(fichier)
		if err != nil {
			return fmt.Errorf("lecture du corpus téléversé : %w", err)
		}
		contenu = lu
	}

	// La saisie est reprise en cas de refus, et elle seule : le contenu d'un
	// fichier n'a rien à faire dans un champ de texte que l'utilisateur n'a
	// pas rempli.
	return metEnFileEtRedirige(e, ouvrier, sourceFournie, lignesDUnReader(bytes.NewReader(contenu)), colle)
}

// metEnFileEtRedirige dépose le travail et renvoie à la page, qui en montre
// l'avancement.
//
// Une redirection et non un fragment, pour la raison déjà écrite sur la
// connexion : une réponse HTMX ne rend pas la mise en page. Et un 303, pour
// qu'un rechargement de la page d'arrivée ne repropose pas de renvoyer le
// formulaire.
func metEnFileEtRedirige(e *core.RequestEvent, ouvrier *ouvrierDAnalyse,
	source string, lignes sourceDeLignes, colle string) error {
	if _, err := ouvrier.metEnFile(source, lignes); err != nil {
		// Le refus du second travail est un refus de formulaire comme les
		// autres, et le seul de la mise en file à en être un : tout le reste
		// remonte tel quel, et ErrorHandler s'en charge.
		if errors.Is(err, errAnalyseDejaEnCours) {
			return rendLEtabli(e, colle, err.Error())
		}
		return err
	}

	return e.Redirect(http.StatusSeeOther, cheminDeLEtabli)
}
