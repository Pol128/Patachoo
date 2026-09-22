package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// L'établi : l'annotation — deux champs, deux niveaux.
//
// Ce fichier porte ce qui se tient sous les écrans : la fonction qui compose
// ce qui pourra partir vers patachoo.org, et l'empreinte qui dit si un verdict
// tient encore. Ni l'une ni l'autre n'appelle de route, et c'est voulu — le
// bouton d'envoi n'est pas dans cette tâche, seule la fonction qu'il appellera
// un jour l'est.

// maxVerdicts borne ce qu'une annotation porte de mots.
//
// La même valeur que maxVerdictsParAnnotation, que la migration pose sur le
// champ : accepter au-delà ici créerait des mots que l'annotation ne pourrait
// ensuite pas référencer. Recopiée plutôt qu'importée, comme maxTags l'est de
// recipes.tags — le paquet des migrations ne dépend pas du serveur.
const maxVerdicts = 20

// --- Ce qui pourra partir ----------------------------------------------------

// tagDeVerdict est un mot du vocabulaire des verdicts, tel qu'une annotation
// le porte : ce qu'on écrit, ce qui s'affiche, et s'il est validé.
type tagDeVerdict struct {
	Slug   string
	Nom    string
	Valide bool
}

// annotationPortee est une annotation vue de la fonction pure : ses deux
// champs, ses mots, sa cible brute et la lecture attendue.
//
// Une structure à elle, et non l'enregistrement PocketBase : la fonction qui
// compose la charge ne doit rien pouvoir aller chercher en base, et c'est ce
// qui rend le test de non-fuite concluant.
type annotationPortee struct {
	// Brut est la ligne du corpus que l'annotation juge. Elle est ici pour
	// qu'on puisse prouver qu'elle ne sort pas.
	Brut string

	// NoteLocale cite la ligne brute : directement réexploitable — c'est la
	// matière d'un jeu annoté — mais elle porte du corpus, donc elle ne sort
	// pas.
	NoteLocale string

	// NotePartageable porte le verdict sur la règle ou sur l'entrée du
	// lexique. Ça parle du lexique et des règles, pas du corpus.
	NotePartageable string

	Verdicts []tagDeVerdict

	// LectureAttendue est la lecture champ à champ qu'il aurait fallu lire.
	// Du côté local, comme la note locale, et pour la même raison.
	LectureAttendue *lectureDUneForme
}

// chargePartageable est ce qu'un envoi vers patachoo.org transporterait : les
// mots validés et la note partageable, rien d'autre.
//
// « Rien d'autre » est le contrat, et il se lit sur la structure : ni la ligne
// brute, ni la note locale, ni la lecture attendue n'y ont de champ. Deux
// colonnes séparées en base ne prouveraient pas qu'on ne lira pas la mauvaise ;
// une structure qui n'a pas de place pour elles, si.
type chargePartageable struct {
	Verdicts []string
	Note     string
}

// composeLaChargePartageable rend ce qui pourra partir, et rien d'autre.
//
// Fonction pure : ni base, ni serveur, ni horloge, ni fichier, et aucun état
// muté — le patron des fonctions pures de signaux.go.
//
// Les mots non validés sont écartés ici, et non à la saisie : un mot est
// partageable par construction — il parle du lexique et des règles — mais il
// est saisi par un humain qui peut y écrire du corpus. La séparation des deux
// champs protège le contenu de l'annotation, pas le nom du mot.
func composeLaChargePartageable(annotation annotationPortee) chargePartageable {
	// Jamais nil : une charge sans verdict est une charge qui n'en porte
	// aucun, et non une charge dont on ne sait rien.
	valides := make([]string, 0, len(annotation.Verdicts))
	for _, verdict := range annotation.Verdicts {
		if !verdict.Valide {
			continue
		}
		valides = append(valides, verdict.Slug)
	}

	return chargePartageable{Verdicts: valides, Note: annotation.NotePartageable}
}

// --- L'empreinte de la lecture -----------------------------------------------

// formeJugee est ce qu'une empreinte juge d'une forme : les quatre champs que
// l'arbitrage nomme, et eux seuls.
//
// Pas d'occurrences : le corpus grossit sans que le parser change d'avis, et
// une empreinte qui les compterait périmerait tous les verdicts de l'instance
// le jour où l'on ajoute une recette. Pas de motif non plus — il ne se lit pas
// sur l'écran qui juge.
type formeJugee struct {
	Brut    string
	Aliment string
	Resolu  bool
	Lecture lectureDUneForme
	Signaux []string
}

// empreinteDeLaLecture rend l'empreinte d'une lecture, groupe ou forme.
//
// Fonction pure, comme la précédente. C'est elle qui tranche « tranché et
// détranché » : le verdict vaut tant que la lecture qu'il jugeait n'a pas
// changé. À la passe suivante, on recalcule et on compare — identique, le
// verdict tient et la cible reste écartée ; différente, elle revient dans
// l'ordre par défaut avec son verdict précédent affiché.
//
// Ni « le verdict vaut pour toujours », qui enterre silencieusement les
// groupes dont la lecture vient justement de bouger, ni « toute montée de
// version périme tout », qui ferait tout rejuger après une correction du seul
// lexique.
//
// Les lignes sont triées après composition et non avant : l'empreinte ne doit
// pas dépendre de l'ordre dans lequel la base a rendu les formes, sans quoi un
// changement de tri périmerait des verdicts sans qu'une seule lecture ait
// bougé.
func empreinteDeLaLecture(formes []formeJugee) string {
	lignes := make([]string, 0, len(formes))
	for _, forme := range formes {
		lignes = append(lignes, ligneDEmpreinte(forme))
	}
	slices.Sort(lignes)

	somme := sha256.Sum256([]byte(strings.Join(lignes, "\x1e")))
	return hex.EncodeToString(somme[:])
}

// ligneDEmpreinte rend ce qu'une forme pèse dans l'empreinte.
//
// Chaque champ est cité par strconv.Quote avant d'être joint : sans cela, deux
// lectures différentes pourraient composer la même chaîne en se partageant
// autrement le séparateur, et un verdict tiendrait sur une lecture qui a
// changé.
//
// Les signaux sont retriés ici bien que signaux() les rende déjà triés : la
// colonne peut avoir été écrite par une autre version, et l'empreinte ne doit
// pas dépendre de cet ordre-là.
func ligneDEmpreinte(forme formeJugee) string {
	signaux := slices.Clone(forme.Signaux)
	slices.Sort(signaux)

	champs := []string{
		forme.Brut,
		forme.Aliment,
		strconv.FormatBool(forme.Resolu),
		quantiteDeLEmpreinte(forme.Lecture.Quantite),
		forme.Lecture.Unite,
		forme.Lecture.Partitif,
		forme.Lecture.Aliment,
		forme.Lecture.Note,
		strconv.FormatBool(forme.Lecture.Optionnel),
		strings.Join(signaux, ","),
	}

	cites := make([]string, 0, len(champs))
	for _, champ := range champs {
		cites = append(cites, strconv.Quote(champ))
	}
	return strings.Join(cites, "\x1f")
}

// quantiteDeLEmpreinte rend une quantité sous une forme stable.
//
// L'absence de quantité se distingue de zéro : « du sel » et « 0 g de sel » ne
// sont pas la même lecture.
func quantiteDeLEmpreinte(quantite *float64) string {
	if quantite == nil {
		return ""
	}
	return strconv.FormatFloat(*quantite, 'g', -1, 64)
}

// empreinteDuGroupe rend l'empreinte de la lecture d'un groupe dans une passe.
//
// Le groupe se désigne par son aliment canonique, qui est sa clé naturelle —
// la même que celle d'une annotation de groupe.
func empreinteDuGroupe(app core.App, analyse, aliment string) (string, error) {
	formes, err := formesJugees(app, analyse,
		dbx.NewExp("food = {:aliment}", dbx.Params{"aliment": aliment}))
	if err != nil {
		return "", fmt.Errorf("lecture du groupe %q : %w", aliment, err)
	}
	return empreinteDeLaLecture(formes), nil
}

// empreinteDeLaForme rend l'empreinte de la lecture d'une seule ligne.
//
// La ligne brute est sa clé naturelle : l'index unique (analysis, raw) garantit
// qu'une passe n'en porte qu'une.
func empreinteDeLaForme(app core.App, analyse, brut string) (string, error) {
	formes, err := formesJugees(app, analyse,
		dbx.NewExp("raw = {:brut}", dbx.Params{"brut": brut}))
	if err != nil {
		return "", fmt.Errorf("lecture de la forme %q : %w", brut, err)
	}
	return empreinteDeLaLecture(formes), nil
}

// formesJugees relit les formes d'une passe que la condition retient, réduites
// à ce que l'empreinte juge.
//
// Une requête construite plutôt que FindRecordsByFilter : la condition est
// écrite par ce fichier et non par un utilisateur, mais les valeurs, elles,
// viennent de l'URL — elles passent par dbx.Params, jamais par concaténation.
func formesJugees(app core.App, analyse string, condition dbx.Expression) ([]formeJugee, error) {
	formes := []*core.Record{}
	err := app.RecordQuery("analyses_formes").
		AndWhere(dbx.NewExp("analysis = {:analyse}", dbx.Params{"analyse": analyse})).
		AndWhere(condition).
		All(&formes)
	if err != nil {
		return nil, err
	}

	jugees := make([]formeJugee, 0, len(formes))
	for _, forme := range formes {
		jugee, err := jugeLaForme(forme)
		if err != nil {
			return nil, err
		}
		jugees = append(jugees, jugee)
	}
	return jugees, nil
}

// jugeLaForme déplie les deux colonnes JSON d'une forme et ne garde que ce que
// l'empreinte juge.
func jugeLaForme(forme *core.Record) (formeJugee, error) {
	var lu lectureDUneForme
	if err := forme.UnmarshalJSONField("reading", &lu); err != nil {
		return formeJugee{}, fmt.Errorf("lecture de la forme %q : %w", forme.Id, err)
	}

	var signaux []string
	if err := forme.UnmarshalJSONField("signals", &signaux); err != nil {
		return formeJugee{}, fmt.Errorf("signaux de la forme %q : %w", forme.Id, err)
	}

	return formeJugee{
		Brut:    forme.GetString("raw"),
		Aliment: forme.GetString("food"),
		Resolu:  forme.GetBool("resolved"),
		Lecture: lu,
		Signaux: signaux,
	}, nil
}

// --- Le vocabulaire des verdicts ----------------------------------------------

// verdictsDepuisSaisie rend les mots de verdict décrits par une saisie libre,
// en les créant au besoin.
//
// Le mécanisme est celui du carnet — même hook de normalisation, même unicité
// sur le slug —, la collection ne l'est pas : mélanger les deux ferait remonter
// « capture-trop » dans les filtres des recettes.
//
// Un mot neuf naît non validé, et c'est le champ validated du schéma qui le
// pose : rien n'est à faire ici, et c'est justement ce qu'on veut — une valeur
// par défaut qu'un appelant peut oublier n'en est pas une.
func verdictsDepuisSaisie(app core.App, saisie string) ([]*core.Record, error) {
	return motsDepuisSaisie(app, collectionDesVerdicts, maxVerdicts, saisie)
}

// --- Les routes de l'annotation ------------------------------------------------

// cheminDeLAnnotation porte les deux routes du dépôt : le GET ouvre le
// formulaire sur une ligne de la vue agrégée, le POST l'enregistre.
//
// Sous le préfixe de l'établi, posé par la page de lancement. Aucun autre
// préfixe n'est inventé ici.
const cheminDeLAnnotation = cheminDeLEtabli + "/annotation"

// cheminDesSuggestionsDeVerdicts est la route propre du vocabulaire des
// verdicts : celle du carnet ne peut pas servir, elle interroge l'autre
// collection et ne vit pas derrière le droit de l'établi.
const cheminDesSuggestionsDeVerdicts = cheminDeLEtabli + "/verdicts/suggestions"

// Les deux niveaux de l'annotation. Les valeurs sont écrites dans les URL et
// relues telles quelles : ce sont des clés.
const (
	cibleDuGroupe  = "groupe"
	cibleDeLaForme = "forme"
)

// Les champs du formulaire. Le code et les tests les partagent ; le gabarit les
// écrit en dur, comme tous les gabarits du dépôt — un champ mal nommé se lit
// comme un champ vide, et le dépôt serait refusé sans dire pourquoi.
const (
	champDeLaCible           = "cible"
	champDuVerdict           = "verdicts"
	champDeLaNoteLocale      = "note-locale"
	champDeLaNotePartageable = "note-partageable"
)

// parametreDeLaForme porte la ligne brute quand la cible est une forme. La
// passe et l'aliment reprennent les paramètres des autres écrans de l'établi :
// ce sont les mêmes clés, et deux noms pour la même chose finiraient par
// diverger.
const parametreDeLaForme = "forme"

// Les six champs de la lecture attendue, en miroir de analyses_formes.reading.
// Préfixés, pour ne jamais se confondre avec la lecture que le parser a faite.
const (
	champDeLaQuantiteAttendue = "attendu-quantite"
	champDeLUniteAttendue     = "attendu-unite"
	champDuPartitifAttendu    = "attendu-partitif"
	champDeLAlimentAttendu    = "attendu-aliment"
	champDeLaNoteAttendue     = "attendu-note"
	champDeLOptionnelAttendu  = "attendu-optionnel"
)

// Les deux refus du dépôt. Ils disent ce qui n'allait pas et ce qu'il faut
// faire, sans jamais reprendre la saisie dans leur texte : c'est le champ qui
// la reprend, où le gabarit l'échappe.
const (
	messageAnnotationVide   = "Cette annotation ne dit rien : posez un verdict, une note, ou la lecture attendue."
	messageCibleIntrouvable = "Cette analyse ne porte pas la cible annotée : elle a pu être relue depuis."
)

// champDesVerdicts est le champ de saisie par mots de l'établi.
//
// Même mécanisme que celui du carnet, autre collection, autre gabarit, autre
// route — et derrière le droit de l'établi plutôt que derrière la seule
// session.
var champDesVerdicts = champDeMots{
	collection: collectionDesVerdicts,
	champ:      champDuVerdict,
	bloc:       "etabli-verdicts-saisie.html",
	chemin:     cheminDesSuggestionsDeVerdicts,
}

// gabaritsDeLAnnotation nomme les blocs que le formulaire entraîne avec lui :
// il inclut le champ de verdict, qui vit dans son propre fichier pour pouvoir
// être rerendu seul par la route des suggestions.
//
// Une variable plutôt qu'une liste recopiée à chaque appel de rendre : un
// gabarit oublié quelque part ne se voit qu'au rendu de cette page-là.
var gabaritsDeLAnnotation = []string{"etabli-annotation.html", "etabli-verdicts-saisie.html"}

// gabaritsDeLaLigneDUnGroupe y ajoute la ligne de la vue agrégée, qui s'ouvre
// sur le formulaire.
var gabaritsDeLaLigneDUnGroupe = append([]string{"etabli-groupe-ligne.html"}, gabaritsDeLAnnotation...)

// gabaritsDuBlocDeLaForme y ajoute la zone d'annotation de la page détail.
var gabaritsDuBlocDeLaForme = append([]string{"etabli-forme-annotation.html"}, gabaritsDeLAnnotation...)

// cibleDAnnotation dit ce qu'une annotation juge : un groupe par son aliment
// canonique, une forme par sa ligne brute.
//
// Le niveau est porté explicitement et ne se déduit pas d'un champ rempli : la
// clé du groupe des lignes dont aucun aliment n'a été lu est la chaîne vide, et
// « l'aliment est vide donc c'est une forme » serait faux là précisément.
type cibleDAnnotation struct {
	Niveau  string
	Aliment string
	Brut    string
}

// estUnGroupe dit si la cible est un groupe. Tout ce qui n'est pas le groupe
// est la forme : un niveau inconnu — une URL tapée de travers — retombe sur la
// forme, dont la clé, elle, ne peut pas être vide.
func (c cibleDAnnotation) estUnGroupe() bool {
	return c.Niveau == cibleDuGroupe
}

// cle rend la clé naturelle de la cible, celle qui s'écrit en base.
func (c cibleDAnnotation) cle() string {
	if c.estUnGroupe() {
		return c.Aliment
	}
	return c.Brut
}

// lisLaCible lit la cible d'une saisie — chaîne de requête pour le GET, corps
// du formulaire pour le POST. FormValue lit les deux.
func lisLaCible(r *http.Request) cibleDAnnotation {
	return cibleDAnnotation{
		Niveau:  r.FormValue(champDeLaCible),
		Aliment: r.FormValue(parametreDeLAliment),
		Brut:    r.FormValue(parametreDeLaForme),
	}
}

// brancheLesAnnotations pose les trois routes, toutes derrière le droit
// d'entrer dans l'établi (PATA-128).
//
// La seule écriture de l'établi, et elle est gardée comme le lancement d'une
// passe : le droit d'abord — sa priorité le fait passer avant toute lecture du
// corps —, puis le jeton anti-rejeu, comme tout POST du dépôt.
func brancheLesAnnotations(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET(cheminDeLAnnotation, fragmentDAnnotation).Bind(exigeUnCurateur())
	routeur.POST(cheminDeLAnnotation, poseUneAnnotation).
		Bind(exigeLeJetonAntiRejeu(), exigeUnCurateur())
	routeur.GET(champDesVerdicts.chemin, suggestionsDeMots(champDesVerdicts)).
		Bind(exigeUnCurateur())
}

// annotationAffichee est une annotation posée, telle qu'un écran la montre.
//
// Les deux notes y sont deux champs distincts, et les gabarits les rendent dans
// deux blocs distincts : deux colonnes séparées en base ne servent à rien si la
// page les mêle.
type annotationAffichee struct {
	Verdicts []tagDeVerdict
	Moteur   string

	NoteLocale      string
	NotePartageable string

	// Attendue n'est remplie que par une annotation de forme : le gabarit
	// s'ouvre sur un {{with}}, donc une annotation de groupe n'écrit pas de
	// bloc vide.
	Attendue *lectureAffichee

	Date string
}

// formulaireDAnnotation est ce que le fragment reçoit. Il sert aux deux
// niveaux : la cible change, pas le formulaire.
type formulaireDAnnotation struct {
	Action         string
	JetonAntiRejeu string

	Analyse string
	Cible   string
	Aliment string
	Forme   string

	Verdicts saisieDeMots

	// AvecLectureAttendue ouvre les six champs de la lecture attendue. Au
	// niveau de la forme seulement : « il fallait lire 2 oignons jaunes » n'a
	// pas de sens sur un groupe, qui n'est pas une ligne.
	AvecLectureAttendue bool

	Message string
}

// formulairePourLaCible compose le formulaire d'une cible donnée.
func formulairePourLaCible(e *core.RequestEvent, analyse string, cible cibleDAnnotation, message string) *formulaireDAnnotation {
	return &formulaireDAnnotation{
		Action:              cheminDeLAnnotation,
		JetonAntiRejeu:      jetonAntiRejeuCourant(e),
		Analyse:             analyse,
		Cible:               cible.Niveau,
		Aliment:             cible.Aliment,
		Forme:               cible.Brut,
		AvecLectureAttendue: !cible.estUnGroupe(),
		Message:             message,
	}
}

// fragmentDAnnotation ouvre le formulaire sur une ligne de la vue agrégée.
//
// Le groupe seul passe par ici : sur la page détail, le formulaire est rendu
// avec la fiche, qui n'a qu'une forme à annoter et n'a donc rien à ouvrir.
func fragmentDAnnotation(e *core.RequestEvent) error {
	passe, err := laPasseAffichee(e.App, strings.TrimSpace(e.Request.URL.Query().Get(parametreDeLAnalyse)))
	if err != nil {
		return err
	}
	if passe == nil {
		// Aucune passe terminée : il n'y a rien à annoter, et la vue agrégée
		// le dit déjà mieux qu'un formulaire vide.
		return e.Redirect(http.StatusSeeOther, cheminDesAlimentsDeLEtabli)
	}

	cible := lisLaCible(e.Request)
	cible.Niveau = cibleDuGroupe

	return rendLaLigneDuGroupe(e, &groupeDAliment{
		Aliment:    cible.Aliment,
		Formulaire: formulairePourLaCible(e, passe.Id, cible, ""),
	})
}

// poseUneAnnotation enregistre le verdict, puis rerend la cible avec sa marque.
//
// La validation d'abord, l'écriture ensuite : un dépôt refusé ne doit laisser
// aucune annotation derrière lui, fût-elle vide — c'est elle qui trancherait
// un groupe que personne n'a jugé.
func poseUneAnnotation(e *core.RequestEvent) error {
	passe, err := laPasseAffichee(e.App, strings.TrimSpace(e.Request.PostFormValue(parametreDeLAnalyse)))
	if err != nil {
		return err
	}
	if passe == nil {
		return e.Redirect(http.StatusSeeOther, cheminDesAlimentsDeLEtabli)
	}

	cible := lisLaCible(e.Request)
	locale := strings.TrimSpace(e.Request.PostFormValue(champDeLaNoteLocale))
	partageable := strings.TrimSpace(e.Request.PostFormValue(champDeLaNotePartageable))
	saisie := e.Request.PostFormValue(champDuVerdict)
	attendue := lectureAttendueSoumise(e.Request, cible)

	// La cible est relue dans la passe jugée, et pour deux raisons à la fois :
	// c'est elle qui dit que la cible existe, et c'est sa lecture qui donne
	// l'empreinte que l'annotation recopie.
	lecture, err := lectureDeLaCible(e.App, passe.Id, cible)
	if err != nil {
		return err
	}

	if refus := refusDeLAnnotation(len(lecture), saisie, locale, partageable, attendue); refus != "" {
		return rendLeFormulaireRefuse(e, passe.Id, cible, refus)
	}

	verdicts, err := verdictsDepuisSaisie(e.App, saisie)
	if err != nil {
		// Une saisie hors borne est un refus de formulaire, pas une panne : le
		// curateur en a écrit trop, et il peut en retirer.
		return rendLeFormulaireRefuse(e, passe.Id, cible, err.Error())
	}

	if err := ecritLAnnotation(e.App, passe, cible, verdicts, locale, partageable, attendue,
		empreinteDeLaLecture(lecture)); err != nil {
		return err
	}

	return rendLaCibleAnnotee(e, passe, cible)
}

// refusDeLAnnotation nomme ce qu'on reproche au dépôt, ou rend "" s'il est
// recevable.
//
// Une annotation qui ne dit rien n'est pas une annotation : elle n'apprend rien
// et, sans verdict, elle ne tranche même pas sa cible — elle ne ferait
// qu'encombrer la table.
func refusDeLAnnotation(formesDeLaCible int, saisie, locale, partageable string, attendue *lectureDUneForme) string {
	if formesDeLaCible == 0 {
		return messageCibleIntrouvable
	}
	if strings.TrimSpace(saisie) == "" && locale == "" && partageable == "" && attendue == nil {
		return messageAnnotationVide
	}
	return ""
}

// lectureAttendueSoumise rend la lecture attendue que le formulaire porte, ou
// nil s'il n'en porte aucune.
//
// Au niveau de la forme seulement : « il fallait lire 2 oignons jaunes » décrit
// une ligne, et un groupe n'en est pas une. Un champ envoyé quand même — une
// requête forgée à la main — est ignoré plutôt que refusé : il n'y a rien à
// protéger, seulement une colonne à ne pas remplir de travers.
func lectureAttendueSoumise(r *http.Request, cible cibleDAnnotation) *lectureDUneForme {
	if cible.estUnGroupe() {
		return nil
	}

	attendue := lectureDUneForme{
		Quantite:  quantiteSoumise(r.PostFormValue(champDeLaQuantiteAttendue)),
		Unite:     strings.TrimSpace(r.PostFormValue(champDeLUniteAttendue)),
		Partitif:  strings.TrimSpace(r.PostFormValue(champDuPartitifAttendu)),
		Aliment:   strings.TrimSpace(r.PostFormValue(champDeLAlimentAttendu)),
		Note:      strings.TrimSpace(r.PostFormValue(champDeLaNoteAttendue)),
		Optionnel: r.PostFormValue(champDeLOptionnelAttendu) != "",
	}
	if attendue == (lectureDUneForme{}) {
		return nil
	}
	return &attendue
}

// quantiteSoumise lit la quantité attendue, ou nil quand le champ est vide ou
// illisible.
//
// L'absence se distingue de zéro : « du sel » et « 0 g de sel » ne sont pas la
// même lecture. Une saisie illisible est traitée comme une absence plutôt que
// comme un refus : le reste de l'annotation vaut d'être gardé.
//
// La virgule décimale est acceptée : c'est ainsi qu'on écrit une quantité en
// français, et le refuser ferait perdre la saisie sans rien dire.
func quantiteSoumise(saisie string) *float64 {
	saisie = strings.TrimSpace(saisie)
	if saisie == "" {
		return nil
	}

	valeur, err := strconv.ParseFloat(strings.ReplaceAll(saisie, ",", "."), 64)
	if err != nil {
		return nil
	}
	return &valeur
}

// lectureDeLaCible relit, dans la passe jugée, les formes que la cible désigne.
func lectureDeLaCible(app core.App, analyse string, cible cibleDAnnotation) ([]formeJugee, error) {
	if cible.estUnGroupe() {
		return formesJugees(app, analyse,
			dbx.NewExp("food = {:aliment}", dbx.Params{"aliment": cible.Aliment}))
	}
	return formesJugees(app, analyse,
		dbx.NewExp("raw = {:brut}", dbx.Params{"brut": cible.Brut}))
}

// ecritLAnnotation enregistre le verdict, avec l'empreinte de ce qu'il jugeait.
//
// Les deux empreintes sont recopiées plutôt que relues à travers la relation :
// celle de la passe dit quel parser était jugé, celle de la lecture dit ce qui
// était lu. L'une et l'autre doivent survivre à la passe suivante, qui n'aura
// ni la même version ni les mêmes lignes.
func ecritLAnnotation(app core.App, passe *core.Record, cible cibleDAnnotation,
	verdicts []*core.Record, locale, partageable string,
	attendue *lectureDUneForme, empreinte string) error {
	collection, err := app.FindCollectionByNameOrId("analyses_annotations")
	if err != nil {
		return fmt.Errorf("collection analyses_annotations : %w", err)
	}

	annotation := core.NewRecord(collection)
	annotation.Set("analysis", passe.Id)
	// Une seule des deux clés est posée : food seul désigne un groupe, et une
	// annotation de forme qui porterait aussi l'aliment trancherait le groupe
	// que personne n'a jugé.
	if cible.estUnGroupe() {
		annotation.Set("food", cible.Aliment)
	} else {
		annotation.Set("raw", cible.Brut)
	}

	identifiants := make([]string, 0, len(verdicts))
	for _, verdict := range verdicts {
		identifiants = append(identifiants, verdict.Id)
	}
	annotation.Set("verdicts", identifiants)

	annotation.Set("local_note", locale)
	annotation.Set("shareable_note", partageable)
	if attendue != nil {
		annotation.Set("expected_reading", *attendue)
	}
	annotation.Set("engine_version", passe.GetString("engine_version"))
	annotation.Set("lexicon_entries", passe.GetInt("lexicon_entries"))
	annotation.Set("reading_digest", empreinte)

	if err := app.Save(annotation); err != nil {
		return fmt.Errorf("écriture de l'annotation : %w", err)
	}
	return nil
}

// --- Ce que les écrans montrent --------------------------------------------------

// rendLeFormulaireRefuse rerend le formulaire avec son reproche, sans rien
// avoir écrit.
func rendLeFormulaireRefuse(e *core.RequestEvent, analyse string, cible cibleDAnnotation, message string) error {
	formulaire := formulairePourLaCible(e, analyse, cible, message)
	if cible.estUnGroupe() {
		return rendLaLigneDuGroupe(e, &groupeDAliment{Aliment: cible.Aliment, Formulaire: formulaire})
	}
	return rendLeBlocDeLaForme(e, &blocDAnnotationDUneForme{Formulaire: formulaire})
}

// rendLaCibleAnnotee rerend la cible avec sa marque : la ligne du groupe pour
// un verdict de groupe, le bloc de la fiche pour un verdict de forme.
//
// C'est la réponse au dépôt, et c'est elle qui fait apparaître la marque sans
// recharger la page.
func rendLaCibleAnnotee(e *core.RequestEvent, passe *core.Record, cible cibleDAnnotation) error {
	if cible.estUnGroupe() {
		groupe, err := leGroupeAnnote(e.App, passe.Id, cible.Aliment)
		if err != nil {
			return err
		}
		return rendLaLigneDuGroupe(e, groupe)
	}

	bloc, err := leBlocDeLaForme(e, passe.Id, cible.Brut)
	if err != nil {
		return err
	}
	return rendLeBlocDeLaForme(e, bloc)
}

// blocDAnnotationDUneForme est la zone d'annotation de la page détail : ce qui
// a déjà été posé, puis le formulaire.
type blocDAnnotationDUneForme struct {
	Annotations []annotationAffichee
	Formulaire  *formulaireDAnnotation
}

// rendLaLigneDuGroupe écrit la ligne seule, formulaire ouvert ou marque posée.
//
// rendLeBlocSeul et non rendre : une ligne de tableau n'est pas une page, et
// cette route n'a donc pas d'arbitrage à faire — c'est le raisonnement déjà
// tenu pour le champ de tags.
func rendLaLigneDuGroupe(e *core.RequestEvent, groupe *groupeDAliment) error {
	return rendLeBlocSeul(e, "etabli-groupe-ligne.html", groupe, gabaritsDeLAnnotation...)
}

// rendLeBlocDeLaForme écrit la zone d'annotation de la fiche, seule.
func rendLeBlocDeLaForme(e *core.RequestEvent, bloc *blocDAnnotationDUneForme) error {
	return rendLeBlocSeul(e, "etabli-forme-annotation.html", bloc, gabaritsDeLAnnotation...)
}

// leBlocDeLaForme compose la zone d'annotation d'une forme : ses annotations,
// et un formulaire vide pour la suivante.
func leBlocDeLaForme(e *core.RequestEvent, analyse, brut string) (*blocDAnnotationDUneForme, error) {
	posees, err := annotationsDeLaForme(e.App, brut)
	if err != nil {
		return nil, err
	}
	affichees, err := annotationsAffichees(e.App, posees)
	if err != nil {
		return nil, err
	}

	cible := cibleDAnnotation{Niveau: cibleDeLaForme, Brut: brut}
	return &blocDAnnotationDUneForme{
		Annotations: affichees,
		Formulaire:  formulairePourLaCible(e, analyse, cible, ""),
	}, nil
}

// leGroupeAnnote rend la ligne d'un groupe telle que la vue agrégée l'écrit.
//
// L'agrégat est refait pour cette clé seule : la réponse au dépôt doit montrer
// la ligne à jour, et non celle que la page portait avant le verdict.
func leGroupeAnnote(app core.App, analyse, aliment string) (*groupeDAliment, error) {
	groupes := []groupeDAliment{}
	err := app.DB().
		Select(
			"analyses_formes.food AS aliment",
			"MAX(analyses_formes.category) AS categorie",
			"MAX(analyses_formes.resolved) AS resolu",
			"SUM(analyses_formes.occurrences) AS occurrences_totales",
			"COUNT(*) AS formes_avalees",
		).
		From("analyses_formes").
		Where(dbx.NewExp("analyses_formes.analysis = {:analyse}", dbx.Params{"analyse": analyse})).
		AndWhere(dbx.NewExp("analyses_formes.food = {:aliment}", dbx.Params{"aliment": aliment})).
		GroupBy("analyses_formes.food").
		All(&groupes)
	if err != nil {
		return nil, fmt.Errorf("groupe %q : %w", aliment, err)
	}
	if len(groupes) == 0 {
		// La cible a disparu entre le dépôt et la relecture : la ligne se rend
		// nue plutôt que par une 500, l'annotation étant déjà écrite.
		groupes = append(groupes, groupeDAliment{Aliment: aliment})
	}

	if err := poseLesSignaux(app, analyse, groupes); err != nil {
		return nil, err
	}
	if err := poseLesAnnotations(app, groupes); err != nil {
		return nil, err
	}
	groupes[0].Detail = lienDesFormesDuGroupe(analyse, aliment)
	groupes[0].Annoter = lienDAnnotationDUnGroupe(analyse, aliment)
	return &groupes[0], nil
}

// lienDAnnotationDUnGroupe rend l'adresse à laquelle le formulaire s'ouvre sur
// la ligne d'un groupe. Écrite une fois, ici, plutôt que composée des deux
// côtés.
func lienDAnnotationDUnGroupe(analyse, aliment string) string {
	valeurs := url.Values{}
	valeurs.Set(parametreDeLAnalyse, analyse)
	valeurs.Set(champDeLaCible, cibleDuGroupe)
	valeurs.Set(parametreDeLAliment, aliment)
	return cheminDeLAnnotation + "?" + valeurs.Encode()
}

// --- La relecture des annotations posées -------------------------------------------

// annotationsDuGroupe rend les annotations posées sur une clé de groupe,
// toutes passes confondues.
//
// Toutes passes confondues, et c'est le critère de survie : un verdict rendu à
// la passe d'hier se retrouve sur le groupe de celle d'aujourd'hui, la clé
// étant l'aliment canonique et non l'identifiant d'une ligne.
//
// Le « food != ” » n'est pas un ornement : une annotation de forme laisse son
// propre champ food vide, et sans ce terme elle remonterait sur le groupe des
// aliments vides que le signal aliment_vide désigne.
func annotationsDuGroupe(app core.App, aliment string) ([]*core.Record, error) {
	return annotationsPosees(app, "food = {:cle} && food != ''", aliment)
}

// annotationsDeLaForme rend celles posées sur une ligne brute.
func annotationsDeLaForme(app core.App, brut string) ([]*core.Record, error) {
	return annotationsPosees(app, "raw = {:cle} && raw != ''", brut)
}

// annotationsPosees relit les annotations d'une cible, la plus récente en tête.
//
// La valeur vient de l'URL : elle passe par params, jamais par concaténation
// dans le filtre.
func annotationsPosees(app core.App, filtre, cle string) ([]*core.Record, error) {
	annotations, err := app.FindRecordsByFilter("analyses_annotations", filtre,
		"-created", 0, 0, dbx.Params{"cle": cle})
	if err != nil {
		return nil, fmt.Errorf("annotations de %q : %w", cle, err)
	}
	return annotations, nil
}

// annotationsAffichees met des annotations en forme pour l'écran, mots
// compris.
//
// Les mots sont relus en une seule requête pour toute la tranche, et non un par
// un : une page de vingt-quatre groupes en porte autant de listes, et une
// requête par liste ferait vingt-quatre allers-retours pour afficher un écran.
func annotationsAffichees(app core.App, annotations []*core.Record) ([]annotationAffichee, error) {
	if len(annotations) == 0 {
		return nil, nil
	}

	mots, err := lesMotsDesVerdicts(app, annotations)
	if err != nil {
		return nil, err
	}

	affichees := make([]annotationAffichee, 0, len(annotations))
	for _, annotation := range annotations {
		portes := make([]tagDeVerdict, 0, 4)
		for _, id := range annotation.GetStringSlice("verdicts") {
			if mot, connu := mots[id]; connu {
				portes = append(portes, mot)
			}
		}

		affichee := annotationAffichee{
			Verdicts:        portes,
			Moteur:          annotation.GetString("engine_version"),
			NoteLocale:      annotation.GetString("local_note"),
			NotePartageable: annotation.GetString("shareable_note"),
			Date:            dateEnFrancais(annotation.GetDateTime("created")),
		}

		if attendue := annotation.GetString("expected_reading"); attendue != "" && attendue != "null" {
			var lue lectureDUneForme
			if err := annotation.UnmarshalJSONField("expected_reading", &lue); err != nil {
				return nil, fmt.Errorf("lecture attendue de l'annotation %q : %w", annotation.Id, err)
			}
			mise := lectureAffichable(lue)
			affichee.Attendue = &mise
		}

		affichees = append(affichees, affichee)
	}
	return affichees, nil
}

// lesMotsDesVerdicts relit, en une requête, les mots que ces annotations
// portent.
func lesMotsDesVerdicts(app core.App, annotations []*core.Record) (map[string]tagDeVerdict, error) {
	identifiants := []string{}
	for _, annotation := range annotations {
		identifiants = append(identifiants, annotation.GetStringSlice("verdicts")...)
	}
	if len(identifiants) == 0 {
		return nil, nil
	}

	trouves, err := app.FindRecordsByIds(collectionDesVerdicts, identifiants)
	if err != nil {
		return nil, fmt.Errorf("mots des verdicts : %w", err)
	}

	mots := make(map[string]tagDeVerdict, len(trouves))
	for _, mot := range trouves {
		mots[mot.Id] = tagDeVerdict{
			Slug:   mot.GetString("slug"),
			Nom:    mot.GetString("name"),
			Valide: mot.GetBool("validated"),
		}
	}
	return mots, nil
}

// poseLesAnnotations remplit les annotations des groupes d'une page, et d'eux
// seuls.
//
// Une requête par groupe, bornée à la page : la clé est un texte libre, il n'y
// a pas d'index sur analyses_annotations.food, et la table est humaine — elle
// grandit d'un verdict à la fois, pas d'une ligne de corpus.
func poseLesAnnotations(app core.App, groupes []groupeDAliment) error {
	for i := range groupes {
		posees, err := annotationsDuGroupe(app, groupes[i].Aliment)
		if err != nil {
			return err
		}
		affichees, err := annotationsAffichees(app, posees)
		if err != nil {
			return err
		}
		groupes[i].Annotations = affichees
	}
	return nil
}

// --- Ce qui tranche un groupe, et ce qui le détranche -----------------------------

// alimentsTranches rend les clés de groupe dont le verdict tient encore pour
// cette passe.
//
// Deux conditions, et les deux comptent. La cible porte au moins un mot de
// verdict : une note sans mot ne tranche pas, c'est ce qui permet de déposer
// une remarque sans retirer le groupe de la file. Et la lecture n'a pas bougé
// depuis : l'empreinte enregistrée est celle de ce que l'annotation jugeait, on
// la recompare à celle de la passe affichée.
//
// Ni « le verdict vaut pour toujours », qui enterrerait silencieusement les
// groupes dont la lecture vient justement de changer, ni « toute montée de
// version périme tout », qui ferait tout rejuger après une correction du seul
// lexique.
//
// En Go plutôt qu'en SQL, et c'est un choix de taille : la comparaison porte
// sur une empreinte d'agrégat, qu'un NOT EXISTS corrélé devrait recalculer pour
// chaque ligne du corpus. Ici, la table des annotations est humaine — elle
// grandit d'un verdict à la fois, pas d'une ligne de corpus —, et les lectures
// relues sont bornées aux seules clés annotées.
func alimentsTranches(app core.App, analyse string) ([]any, error) {
	// Toutes passes confondues : un verdict rendu hier tranche le groupe de la
	// passe d'aujourd'hui, la clé étant l'aliment canonique.
	annotations, err := app.FindRecordsByFilter("analyses_annotations",
		"food != ''", "-created", 0, 0)
	if err != nil {
		return nil, fmt.Errorf("annotations de groupe : %w", err)
	}

	// Par clé, les empreintes que ses verdicts jugeaient. Plusieurs annotations
	// sur une même cible sont permises : il suffit que l'une d'elles tienne
	// encore.
	jugees := map[string][]string{}
	for _, annotation := range annotations {
		if len(annotation.GetStringSlice("verdicts")) == 0 {
			continue
		}
		aliment := annotation.GetString("food")
		jugees[aliment] = append(jugees[aliment], annotation.GetString("reading_digest"))
	}
	if len(jugees) == 0 {
		return nil, nil
	}

	courantes, err := empreintesDesGroupes(app, analyse, jugees)
	if err != nil {
		return nil, err
	}

	tranches := make([]any, 0, len(jugees))
	for aliment, empreintes := range jugees {
		if slices.Contains(empreintes, courantes[aliment]) {
			tranches = append(tranches, aliment)
		}
	}
	return tranches, nil
}

// empreintesDesGroupes rend, pour chaque clé demandée, l'empreinte de sa
// lecture dans cette passe.
//
// Une seule requête pour toutes les clés, et non une par clé : le regroupement
// se fait ensuite en mémoire, sur les seules formes des groupes annotés.
func empreintesDesGroupes(app core.App, analyse string, jugees map[string][]string) (map[string]string, error) {
	aliments := make([]any, 0, len(jugees))
	for aliment := range jugees {
		aliments = append(aliments, aliment)
	}

	formes, err := formesJugees(app, analyse, dbx.In("food", aliments...))
	if err != nil {
		return nil, fmt.Errorf("lecture courante des groupes annotés : %w", err)
	}

	parAliment := map[string][]formeJugee{}
	for _, forme := range formes {
		parAliment[forme.Aliment] = append(parAliment[forme.Aliment], forme)
	}

	empreintes := make(map[string]string, len(jugees))
	for aliment := range jugees {
		// Une clé que la passe ne porte plus a l'empreinte d'un groupe vide,
		// qui ne peut égaler aucune empreinte jugée : le groupe n'est pas
		// affiché de toute façon, et rien ne le tranche à tort.
		empreintes[aliment] = empreinteDeLaLecture(parAliment[aliment])
	}
	return empreintes, nil
}
