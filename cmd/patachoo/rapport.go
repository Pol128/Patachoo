package main

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/Pol128/Patachoo/jsonld"
	"github.com/Pol128/Patachoo/recuperation"
)

// Le suivi d'une fournée : la progression pendant qu'elle tourne, le rapport
// une fois qu'elle est finie.
//
// Une seule route pour les deux — c'est la même question posée à deux moments,
// et c'est le statut du lot qui tranche. Deux routes obligeraient la
// progression à savoir quand basculer sur l'autre, c'est-à-dire à savoir la
// même chose deux fois.
//
// Ce fichier ne fait que lire : il n'émet aucune requête sortante et ne crée
// aucune recette. Tout ce qu'il rend est écrit par l'ouvrier (ouvrier.go).

// cheminDuSuivi porte la route du suivi, dans le prolongement de celle du lot.
// Une seule constante, pour la même raison que cheminDuLot : une URL recopiée
// finit par diverger de celle qui est branchée.
const cheminDuSuivi = cheminDuLot + "/{id}"

// cheminDeLaReprise porte la bascule d'une ligne en échec, dans le prolongement
// du suivi. Même raison qu'au-dessus : une URL recopiée finit par diverger de
// celle qui est branchée.
const cheminDeLaReprise = cheminDuSuivi + "/lignes/{ligne}"

// champDeLaReprise porte l'état coché d'une adresse en échec — ce que
// l'utilisateur déclare, là où status porte ce que l'ouvrier a constaté.
const champDeLaReprise = "handled"

// cadenceDuSuivi est l'intervalle du rafraîchissement HTMX.
//
// Deux secondes : l'ouvrier ne peut pas traiter plus d'une URL par seconde et
// par hôte (cadence.go), donc rafraîchir plus vite ne montrerait rien de plus.
// Le lot ouvert dans un onglet oublié coûte une requête toutes les deux
// secondes, et ça s'arrête à la clôture — le fragment terminé ne redemande
// rien.
const cadenceDuSuivi = "every 2s"

// donneesSuivi est ce que le gabarit du suivi reçoit, dans les deux états.
//
// Une seule structure, et non une par état : les deux partagent le lot, son
// total et son tag, et les séparer obligerait le gabarit à choisir entre deux
// formes avant même de choisir entre deux affichages.
type donneesSuivi struct {
	donneesPage

	// Lien est l'adresse à laquelle le fragment se redemande, et Cadence
	// l'intervalle du hx-trigger : le gabarit ne les recompose pas, il les
	// écrit tels quels.
	Lien    string
	Cadence string
	Termine bool
	Total   int

	// Tag est nil quand la fournée n'en a plus : le gabarit s'ouvre alors sur
	// un {{with}} et n'écrit ni lien ni libellé orphelin.
	Tag *lienDeFait

	// Ce que la progression seule affiche : le compte de ce qui a son sort,
	// l'état du lot dans la file, et le titre de la dernière recette entrée.
	Traitees int
	Etat     string
	Derniere string

	// Ce que le rapport seul affiche : les trois comptes qui font le total,
	// le détail des échecs par famille, puis les adresses une à une.
	Comptes []compteDuRapport
	Detail  []compteDuRapport
	Echecs  []echecDuRapport

	// Reprises est le nombre d'adresses en échec que l'utilisateur a cochées,
	// sur le total des échecs — que le gabarit tire de Echecs. C'est ce qui
	// fait dire à l'écran, d'un coup d'œil, ce qui reste à faire.
	Reprises int
}

// compteDuRapport est une famille et son effectif.
type compteDuRapport struct {
	Libelle string
	Compte  int
}

// echecDuRapport est une adresse qui n'a pas abouti, et ce qu'on en sait.
type echecDuRapport struct {
	URL string
	// Cause est le libellé français, jamais la valeur enregistrée : c'est ce
	// que quelqu'un lit pour décider s'il reprend l'adresse à la main.
	Cause string

	// Lien est l'adresse à laquelle la case de cette ligne se poste, composée
	// ici et non par le gabarit : une URL recopiée diverge le jour où la route
	// bouge, et celle-ci en porte deux identifiants.
	Lien string

	// Reprise dit que l'utilisateur a déclaré s'en être occupé. L'adresse
	// reste en échec pour autant — c'est une déclaration, pas un nouveau sort.
	Reprise bool
}

// lienDuSuivi rend l'adresse du suivi d'un lot.
//
// Écrite ici, et nulle part ailleurs : le gabarit de la confirmation et celui
// de la progression la portent tous les deux, et une adresse recopiée diverge
// le jour où la route bouge (PATA-44).
func lienDuSuivi(lot string) string {
	return cheminDuLot + "/" + lot
}

// lienDeLaReprise rend l'adresse à laquelle la case d'une ligne se poste.
//
// Écrite ici pour la même raison que la précédente, et à partir d'elle : les
// deux routes se suivent, et une adresse recomposée à la main les laisserait
// diverger.
func lienDeLaReprise(lot, ligne string) string {
	return lienDuSuivi(lot) + "/lignes/" + ligne
}

// suiviDuLot rend la progression ou le rapport, selon l'état du lot.
func suiviDuLot(e *core.RequestEvent) error {
	lot := leLotDuCompte(e)
	if lot == nil {
		return pageLotIntrouvable(e)
	}

	return rendLeSuivi(e, lot)
}

// leLotDuCompte rend la fournée que l'URL désigne, si elle est bien celle du
// compte connecté, et nil sinon.
//
// La propriété se vérifie ici, dans le code de la route : ces pages sont
// servies par notre code Go, que les règles de collection ne gardent pas — et
// les collections de l'import en lot n'en ont d'ailleurs aucune, elles sont
// fermées à l'API REST.
//
// Un nil plutôt qu'une 404 rendue sur place, comme laNoteDemandee : le choix
// entre document et fragment appartient à rendre, et une fonction de recherche
// qui écrirait la réponse ne pourrait plus être appelée deux fois.
func leLotDuCompte(e *core.RequestEvent) *core.Record {
	lot, err := e.App.FindRecordById("imports", e.Request.PathValue("id"))
	// Une absence dans les deux cas, et non un refus pour le second :
	// l'existence de la fournée d'un autre compte n'est pas une information à
	// donner par un code de statut.
	if err != nil || lot.GetString(champAuteur) != e.Auth.Id {
		return nil
	}
	return lot
}

// rendLeSuivi met en forme l'état du lot et le rend — fragment ou document,
// c'est rendre qui tranche.
func rendLeSuivi(e *core.RequestEvent, lot *core.Record) error {
	donnees, err := suiviDe(e.App, lot)
	if err != nil {
		return err
	}
	return rendre(e, "import-lot-suivi.html", "import-lot-suivi-corps.html", donnees)
}

// basculeLaReprise inverse la case d'une adresse en échec, puis rend le suivi.
//
// Elle ne regarde pas le statut du lot : seul le rapport — donc un lot terminé
// — offre le bouton, et ajouter un refus pour un lot en cours coûterait une
// règle et un test sans rien protéger.
//
// Le rendu passe par rendre, qui choisit seul entre le fragment et le
// document : la case fonctionne donc aussi sans JavaScript, HTMX ne faisant
// qu'éviter le rechargement.
func basculeLaReprise(e *core.RequestEvent) error {
	lot := leLotDuCompte(e)
	if lot == nil {
		return pageLotIntrouvable(e)
	}

	ligne, err := e.App.FindRecordById("import_urls", e.Request.PathValue("ligne"))
	// La ligne doit appartenir au lot cité, et pas seulement exister : sans ce
	// second contrôle, la propriété porterait sur un lot et l'écriture sur un
	// autre.
	if err != nil || ligne.GetString("batch") != lot.Id {
		return pageLotIntrouvable(e)
	}

	ligne.Set(champDeLaReprise, !ligne.GetBool(champDeLaReprise))
	if err := e.App.Save(ligne); err != nil {
		return fmt.Errorf("reprise de la ligne %s : %w", ligne.Id, err)
	}

	return rendLeSuivi(e, lot)
}

// pageLotIntrouvable répond par une page lisible, et non par une page vide.
func pageLotIntrouvable(e *core.RequestEvent) error {
	return rendreAvecStatut(e, http.StatusNotFound,
		"import-lot-introuvable.html", "import-lot-introuvable-corps.html", &donneesPage{
			Titre: "Fournée introuvable — Patachoo",
		})
}

// suiviDe lit l'état du lot et le met en forme pour le gabarit.
func suiviDe(app core.App, lot *core.Record) (*donneesSuivi, error) {
	lignes, err := app.FindAllRecords("import_urls", dbx.HashExp{"batch": lot.Id})
	if err != nil {
		return nil, fmt.Errorf("lignes du lot %s : %w", lot.Id, err)
	}
	// Dans l'ordre de la liste collée, et non dans celui des écritures : c'est
	// celui que l'utilisateur reconnaîtra dans le rapport.
	trieParPosition(lignes)

	donnees := &donneesSuivi{
		donneesPage: donneesPage{Titre: "Import en lot — Patachoo"},
		Lien:        lienDuSuivi(lot.Id),
		Cadence:     cadenceDuSuivi,
		Termine:     lot.GetString("status") == statutTermine,
		Total:       len(lignes),
	}
	if donnees.Tag, err = tagDuLot(app, lot); err != nil {
		return nil, err
	}

	if donnees.Termine {
		donnees.Comptes, donnees.Detail, donnees.Echecs = leRapport(lignes)
		donnees.Reprises = reprises(donnees.Echecs)
		return donnees, nil
	}

	donnees.Traitees = traitees(lignes)
	if donnees.Etat, err = etatDuLot(app, lot); err != nil {
		return nil, err
	}
	if donnees.Derniere, err = derniereImportee(app, lignes); err != nil {
		return nil, err
	}
	return donnees, nil
}

// tagDuLot rend le nom du tag de la fournée et le lien vers la liste filtrée,
// ou nil si la fournée n'en a plus.
//
// creeLeLot en pose un dans la même transaction que le lot, mais un tag
// supprimé depuis l'administration vide la relation : le rapport se rend alors
// sans lui plutôt que de refuser de se rendre. L'absence, et elle seule — une
// panne de lecture remonte, sous peine de rendre un rapport normal auquel il
// manque son fil (PATA-44).
func tagDuLot(app core.App, lot *core.Record) (*lienDeFait, error) {
	id := lot.GetString("tag")
	if id == "" {
		return nil, nil
	}

	tag, err := app.FindRecordById("tags", id)
	if err != nil {
		return nil, fmt.Errorf("tag du lot %s : %w", lot.Id, err)
	}
	return &lienDeFait{
		URL:   lienVersLeTag(tag.GetString("slug")),
		Texte: tag.GetString("name"),
	}, nil
}

// traitees compte les lignes qui ont leur sort, quel qu'il soit : une adresse
// déjà présente et une adresse en échec sont traitées toutes les deux.
func traitees(lignes []*core.Record) int {
	traitees := 0
	for _, ligne := range lignes {
		if estTraitee(ligne.GetString("status")) {
			traitees++
		}
	}
	return traitees
}

// reprises compte les adresses en échec que l'utilisateur a cochées. Sur les
// échecs seuls, et non sur la fournée : le rapport ne liste une à une que
// celles-là, et elles seules portent une case.
func reprises(echecs []echecDuRapport) int {
	comptees := 0
	for _, echec := range echecs {
		if echec.Reprise {
			comptees++
		}
	}
	return comptees
}

func estTraitee(statut string) bool {
	return statut != statutAFaire && statut != statutEnCours
}

// etatDuLot dit si la fournée progresse ou attend son tour.
//
// L'ouvrier mène les lots strictement l'un après l'autre, dans l'ordre de leur
// création : une fournée lente retient donc toutes les suivantes. Sans cette
// distinction, un compteur figé à zéro passerait pour une panne alors que le
// lot n'a simplement pas commencé.
func etatDuLot(app core.App, lot *core.Record) (string, error) {
	ouverts, err := app.FindRecordsByFilter("imports", "status = {:statut}", "created", 0, 0,
		dbx.Params{"statut": statutEnCours})
	if err != nil {
		return "", fmt.Errorf("lots en cours : %w", err)
	}

	// Le premier des lots ouverts est celui que l'ouvrier mène ; les autres
	// attendent derrière lui.
	if len(ouverts) > 0 && ouverts[0].Id != lot.Id {
		return "en attente de son tour", nil
	}
	return "import en cours", nil
}

// derniereImportee rend le titre de la dernière recette entrée, ou "".
//
// Ce n'est pas un ornement : c'est ce qui distingue une fournée qui avance
// d'un compteur qui bouge. Et c'est du texte venu d'un site tiers, donc le
// gabarit l'échappe comme le reste.
//
// La plus récemment écrite, et non la dernière de la liste : les files
// progressent en parallèle, une par hôte, et l'ordre de saisie n'est pas
// l'ordre d'arrivée.
//
// Une recette supprimée depuis vide la relation de sa ligne, qui est alors
// sautée : la progression se tait sur la dernière entrée. Là encore, l'absence
// seule est tolérée — une panne de lecture remonte.
func derniereImportee(app core.App, lignes []*core.Record) (string, error) {
	var derniere *core.Record
	for _, ligne := range lignes {
		if ligne.GetString("status") != statutImportee || ligne.GetString("recipe") == "" {
			continue
		}
		if derniere == nil || !ligne.GetDateTime("updated").Before(derniere.GetDateTime("updated")) {
			derniere = ligne
		}
	}
	if derniere == nil {
		return "", nil
	}

	recette, err := app.FindRecordById("recipes", derniere.GetString("recipe"))
	if err != nil {
		return "", fmt.Errorf("recette entrée par la ligne %s : %w", derniere.Id, err)
	}
	return recette.GetString("title"), nil
}

// leRapport compte les lignes par famille et liste les adresses en échec.
func leRapport(lignes []*core.Record) (comptes, detail []compteDuRapport, echecs []echecDuRapport) {
	parFamille := map[string]int{}
	importees, dejaPresentes := 0, 0

	for _, ligne := range lignes {
		switch ligne.GetString("status") {
		case statutImportee:
			importees++
		case statutDejaPresente:
			dejaPresentes++
		case statutEchec:
			cause := ligne.GetString("cause")
			parFamille[familleDeLaCause(cause)]++
			echecs = append(echecs, echecDuRapport{
				URL:     ligne.GetString("url"),
				Cause:   libelleDeLaCause(cause, ligne.GetInt("code")),
				Lien:    lienDeLaReprise(ligne.GetString("batch"), ligne.Id),
				Reprise: ligne.GetBool(champDeLaReprise),
			})
		}
	}

	comptes = []compteDuRapport{
		{Libelle: "Importées", Compte: importees},
		{Libelle: "Déjà présentes", Compte: dejaPresentes},
		{Libelle: "En échec", Compte: len(echecs)},
	}
	// Les familles dans un ordre fixe, y compris à zéro : un rapport dont les
	// lignes apparaissent et disparaissent d'une fournée à l'autre ne se
	// compare pas au précédent.
	for _, famille := range lesFamillesDEchec {
		detail = append(detail, compteDuRapport{Libelle: famille, Compte: parFamille[famille]})
	}
	return comptes, detail, echecs
}

// Les familles d'échec, dans l'ordre où le rapport les affiche.
//
// « Refusée par la politique de sécurité » est à part des injoignables, et
// ce n'est pas un détail de présentation : c'est nous qui avons refusé, pas le
// site, et l'utilisateur n'a rien à réessayer. « Enregistrement impossible »
// l'est pour la raison inverse — c'est notre base qui a refusé l'écriture, et
// la sous-tâche 3 l'écrit comme onzième cause.
const (
	familleInjoignables   = "Injoignables"
	familleSansBalisage   = "Sans balisage exploitable"
	famillePolitique      = "Refusées par la politique de sécurité"
	familleEnregistrement = "Enregistrement impossible"
)

var lesFamillesDEchec = []string{
	familleInjoignables,
	familleSansBalisage,
	famillePolitique,
	familleEnregistrement,
}

// familleDeLaCause range une cause dans la famille qui décide de la réaction.
//
// Une cause inconnue va aux injoignables : la colonne est un texte libre — la
// liste appartient au code qui l'écrit, pas à la migration —, et une famille
// de plus par cause future rendrait le rapport illisible.
func familleDeLaCause(cause string) string {
	switch {
	case slices.Contains([]string{jsonld.SansRecette, jsonld.AucunBalisage, jsonld.JSONInvalide, jsonld.TitreAbsent}, cause):
		return familleSansBalisage
	case cause == recuperation.RefuseeParPolitique:
		return famillePolitique
	case cause == causeEnregistrement:
		return familleEnregistrement
	default:
		return familleInjoignables
	}
}

// libelleDeLaCause dit, en français, ce qui est arrivé à une adresse.
//
// Un libellé par cause, et deux causes n'en partagent jamais un : c'est ce qui
// fait la différence entre un rapport et un compteur d'erreurs. Aucun ne dit
// « une erreur est survenue » — sur une fournée dont un quart des adresses
// échoue, ce serait dire que rien ne s'est passé.
func libelleDeLaCause(cause string, code int) string {
	switch cause {
	case recuperation.Injoignable:
		return "Site injoignable : aucune réponse à cette adresse."
	case recuperation.RefusHTTP:
		// Le code obtenu, parce que 403 anti-bot et 404 n'appellent pas la
		// même réaction : l'un se reprend à la main, l'autre est perdu.
		return fmt.Sprintf("Refus du site : code HTTP %d.", code)
	case recuperation.RefuseeParPolitique:
		return "Adresse refusée par la politique de sécurité : elle mène au réseau interne."
	case recuperation.RobotsInterdit:
		return "Le robots.txt du site interdit de récupérer cette page."
	case recuperation.DelaiDepasse:
		return "Le site a mis trop de temps à répondre."
	case recuperation.TailleMax:
		return "Page trop lourde : la lecture s'est arrêtée au plafond."
	case jsonld.SansRecette:
		return "Balisage trouvé, mais aucune recette dedans."
	case jsonld.AucunBalisage:
		return "Aucun balisage de recette sur la page."
	case jsonld.JSONInvalide:
		return "Le balisage de la page est illisible."
	case jsonld.TitreAbsent:
		return "Recette sans titre : rien à enregistrer sous ce nom."
	case causeEnregistrement:
		return "Enregistrement refusé par la base de Patachoo."
	default:
		// Une cause que ce code ne connaît pas encore : elle est nommée telle
		// qu'elle a été enregistrée, plutôt que tue.
		return fmt.Sprintf("Échec enregistré sous la cause « %s ».", cause)
	}
}
