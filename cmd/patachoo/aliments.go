package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// L'établi : la vue agrégée par aliment.
//
// Un groupe par aliment canonique, pour changer l'unité de décision — un
// verdict sur un groupe tranche toutes ses lignes d'un coup. Sans agrégation,
// c'est une décision par forme ; avec, quelques milliers.
//
// Rien d'agrégé n'est stocké : le groupe se calcule par GROUP BY sur
// analyses_formes, comme PATA-122 le dit. La clé est naturelle — l'aliment
// canonique quand la forme s'est résolue, le texte d'aliment tel que lu
// sinon —, et c'est déjà ce que porte la colonne food (moteur : « Aliment
// porte la forme canonique de l'entrée du lexique quand la ligne s'y résout,
// et le texte de la ligne sinon »). C'est la même clé que les annotations de
// groupe.
//
// Cette page ne lit que la table des formes et celle des annotations. Elle
// n'ajoute aucune route en écriture, et ne rouvre pas le lexique : la
// catégorie est relue dans la colonne que l'ouvrier a écrite.

// cheminDesAlimentsDeLEtabli accroche la vue sous le préfixe de l'établi,
// posé par la page de lancement. Aucun autre préfixe n'est inventé ici.
const cheminDesAlimentsDeLEtabli = cheminDeLEtabli + "/aliments"

// Les quatre paramètres de la chaîne de requête. Le code et les tests les
// partagent, et les liens de la page les réécrivent : une chaîne recopiée
// finirait par diverger d'une lettre, et un paramètre mal nommé se lit comme
// un paramètre absent — c'est-à-dire comme rien.
const (
	parametreDeLAnalyse        = "analyse"
	parametreDuTri             = "tri"
	parametreDeLaPage          = "page"
	parametreDeLaListeComplete = "liste"
)

// valeurDeLaListeComplete ouvre la liste sans filtre. Une valeur nommée plutôt
// qu'un booléen implicite : « liste=complete » se lit dans une URL partagée.
const valeurDeLaListeComplete = "complete"

// Les trois tris qui valent mieux que la fréquence.
//
// Les valeurs sont écrites dans les URL et relues telles quelles : ce sont des
// clés, et elles se renomment au prix de tous les liens déjà partagés.
const (
	triParDispersion = "dispersion"
	triParCategorie  = "categorie"
	triParRarete     = "rarete"
)

// criteresDesAliments porte ce que la chaîne de requête dit de la vue.
//
// Le patron de criteres (recettes.go), et pour la même raison : un seul type,
// un seul lecteur, et des liens qui se réécrivent sans que la page ait à
// recomposer une URL à chaque fois.
type criteresDesAliments struct {
	// Analyse est l'identifiant de la passe demandée, ou "" pour la dernière
	// terminée. Jamais résolu ici : c'est la page qui va le chercher, et qui
	// retombe sur le défaut quand il ne désigne rien.
	Analyse string

	// Tri est l'une des trois clés, ou "" pour l'ordre par défaut.
	Tri string

	Page int

	// Tout ouvre la liste complète : ni le filtre du signal, ni celui des
	// groupes déjà tranchés. Elle reste atteignable, elle ne s'ouvre pas par
	// défaut.
	Tout bool
}

// lisLesCriteresDesAliments est le seul endroit où la chaîne de requête est lue.
//
// Un paramètre malmené ne produit pas d'erreur : un tri inconnu retombe sur
// l'ordre par défaut, une page non numérique, nulle ou négative sur la
// première. Refuser vaudrait une 500 pour un lien mal recopié — c'est déjà la
// règle de lisLesCriteres, et la vue n'en invente pas une autre.
func lisLesCriteresDesAliments(r *http.Request) criteresDesAliments {
	requete := r.URL.Query()

	page, err := strconv.Atoi(requete.Get(parametreDeLaPage))
	if err != nil || page < 1 {
		page = 1
	}
	// La même borne que la liste des recettes, et pour la même raison : le
	// décalage se calcule par (page-1)*parPage, et un nombre démesuré le
	// ferait déborder en négatif — rendant la première page à qui a demandé
	// la dernière.
	if page > pageMax {
		page = pageMax
	}

	// Un tri hors des trois est ignoré plutôt que rendu en liste vide :
	// l'ensemble est fermé et connu à la compilation, donc la valeur ne peut
	// être qu'une URL tapée de travers.
	tri := requete.Get(parametreDuTri)
	switch tri {
	case triParDispersion, triParCategorie, triParRarete:
	default:
		tri = ""
	}

	return criteresDesAliments{
		Analyse: strings.TrimSpace(requete.Get(parametreDeLAnalyse)),
		Tri:     tri,
		Page:    page,
		Tout:    requete.Get(parametreDeLaListeComplete) == valeurDeLaListeComplete,
	}
}

// lien rend l'adresse de la vue pour ces critères, page comprise.
//
// La page 1 ne s'écrit pas : deux adresses pour la même chose finiraient par
// diverger.
func (c criteresDesAliments) lien(page int) string {
	valeurs := url.Values{}
	if c.Analyse != "" {
		valeurs.Set(parametreDeLAnalyse, c.Analyse)
	}
	if c.Tri != "" {
		valeurs.Set(parametreDuTri, c.Tri)
	}
	if c.Tout {
		valeurs.Set(parametreDeLaListeComplete, valeurDeLaListeComplete)
	}
	if page > 1 {
		valeurs.Set(parametreDeLaPage, strconv.Itoa(page))
	}

	if len(valeurs) == 0 {
		return cheminDesAlimentsDeLEtabli
	}
	return cheminDesAlimentsDeLEtabli + "?" + valeurs.Encode()
}

// lienDuTri rend l'adresse de la même vue sous un autre tri.
//
// La page ne suit pas : changer de tri ramène au premier rang, et rester sur
// la page 4 d'un autre ordre n'a pas de sens. Récepteur par valeur, comme
// lienDuType : la barre entière se construit sur les mêmes critères sans
// jamais les altérer.
func (c criteresDesAliments) lienDuTri(tri string) string {
	c.Tri = tri
	return c.lien(1)
}

// lienDeLaListe rend l'adresse de la même vue, filtrée ou complète.
func (c criteresDesAliments) lienDeLaListe(tout bool) string {
	c.Tout = tout
	return c.lien(1)
}

// groupeDAliment est ce qu'une ligne d'écran montre.
//
// Les quatre premiers champs viennent de l'agrégat SQL ; les signaux sont lus
// à part, pour la raison écrite sur signauxDesGroupes.
type groupeDAliment struct {
	Aliment     string `db:"aliment"`
	Categorie   string `db:"categorie"`
	Resolu      bool   `db:"resolu"`
	Occurrences int    `db:"occurrences_totales"`
	Formes      int    `db:"formes_avalees"`

	Signaux []string `db:"-"`

	// Detail descend sur les formes qui ont produit le groupe (PATA-126).
	// Composé ici et non dans le gabarit : l'adresse est celle de l'autre
	// écran, et elle s'écrit dans un seul endroit du dépôt.
	Detail string `db:"-"`
}

// lienDeTri est une entrée de la barre des tris : de quoi l'afficher, y aller,
// et dire lequel est en cours.
type lienDeTri struct {
	Nom     string
	Adresse string
	Actif   bool
}

// donneesAliments est ce que le gabarit de la vue reçoit.
type donneesAliments struct {
	donneesPage

	// SansAnalyse ouvre le seul autre état de la page : tant qu'aucune passe
	// n'est terminée, elle le dit et renvoie au lancement plutôt que
	// d'afficher une liste vide.
	SansAnalyse bool

	// LienDuLancement est l'adresse de la page de lancement, jamais recomposée
	// dans le gabarit.
	LienDuLancement string

	// Date situe la passe affichée : la vue porte sur une analyse, et laquelle
	// se lit sur l'écran plutôt que dans l'URL.
	Date string

	// Les deux tas, déjà séparés : les groupes résolus, puis ceux dont
	// l'aliment n'a pas été reconnu — la file d'attente du lexique.
	Resolus    []groupeDAliment
	NonResolus []groupeDAliment

	Tris []lienDeTri

	// Tout dit si la liste complète est ouverte, et Bascule mène à l'autre.
	Tout    bool
	Bascule string

	Precedente string
	Suivante   string
}

// brancheLesAliments pose la vue agrégée, en lecture seule et derrière le
// droit d'entrer dans l'établi.
//
// Un GET et rien d'autre : l'établi lit le carnet, il ne le modifie pas, et
// l'annotation — la seule écriture de l'établi — est l'affaire de PATA-127.
//
// La garde est celle des autres écrans, pas une seconde : exigeUnCurateur,
// qui renvoie le visiteur se connecter et refuse le compte connecté sans le
// droit.
func brancheLesAliments(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET(cheminDesAlimentsDeLEtabli, pageDesAliments).Bind(exigeUnCurateur())
}

// pageDesAliments rend la vue agrégée de la passe demandée.
func pageDesAliments(e *core.RequestEvent) error {
	criteres := lisLesCriteresDesAliments(e.Request)

	passe, err := laPasseAffichee(e.App, criteres.Analyse)
	if err != nil {
		return err
	}
	if passe == nil {
		return rendre(e, "etabli-aliments.html", "etabli-aliments-corps.html", &donneesAliments{
			donneesPage:     donneesPage{Titre: "Les aliments — Patachoo"},
			SansAnalyse:     true,
			LienDuLancement: cheminDeLEtabli,
		})
	}

	groupes, err := groupesDuRang(e.App, passe.Id, criteres)
	if err != nil {
		return err
	}

	// Un groupe de plus que la page, comme recettesDuRang : c'est lui, et lui
	// seul, qui dit s'il existe une page suivante — sans second décompte, donc
	// sans un GROUP BY de plus sur le corpus entier.
	suivante := len(groupes) > parPage
	if suivante {
		groupes = groupes[:parPage]
	}

	if err := poseLesSignaux(e.App, passe.Id, groupes); err != nil {
		return err
	}

	// Le lien du détail porte la passe affichée, et non le paramètre tel
	// qu'il a été lu : descendre d'un groupe ne doit pas pouvoir changer
	// d'analyse en chemin parce qu'une passe s'est terminée entre l'affichage
	// et le clic.
	for i := range groupes {
		groupes[i].Detail = lienDesFormesDuGroupe(passe.Id, groupes[i].Aliment)
	}

	donnees := &donneesAliments{
		donneesPage:     donneesPage{Titre: "Les aliments — Patachoo"},
		LienDuLancement: cheminDeLEtabli,
		Date:            dateEnFrancais(passe.GetDateTime("created")),
		Tris:            barreDesTris(criteres),
		Tout:            criteres.Tout,
		Bascule:         criteres.lienDeLaListe(!criteres.Tout),
	}
	donnees.Resolus, donnees.NonResolus = lesDeuxTas(groupes)

	if criteres.Page > 1 {
		donnees.Precedente = criteres.lien(criteres.Page - 1)
	}
	if suivante {
		donnees.Suivante = criteres.lien(criteres.Page + 1)
	}

	return rendre(e, "etabli-aliments.html", "etabli-aliments-corps.html", donnees)
}

// laPasseAffichee rend l'analyse sur laquelle la vue porte : celle que l'URL
// demande, ou la dernière terminée, ou nil quand l'instance n'en a aucune.
//
// Un identifiant qui ne désigne rien retombe sur le défaut, sans erreur : même
// règle que le tri inconnu et la page malmenée, et pour la même raison — un
// lien mal recopié ne vaut pas une 500.
//
// La dernière *terminée*, et non la dernière créée comme le fait le bloc
// d'avancement : une passe en cours n'a pas fini d'écrire ses formes, et
// l'agrégat qu'on en tirerait changerait sous les yeux du curateur.
func laPasseAffichee(app core.App, demandee string) (*core.Record, error) {
	if demandee != "" {
		// L'erreur est avalée à dessein : « cette passe n'existe pas » est le
		// cas normal d'une URL tapée de travers, et le retour au défaut est la
		// réponse.
		if passe, err := app.FindRecordById("analyses", demandee); err == nil {
			return passe, nil
		}
	}

	passes, err := app.FindRecordsByFilter("analyses", "status = {:statut}", "-created", 1, 0,
		dbx.Params{"statut": statutTermine})
	if err != nil {
		return nil, fmt.Errorf("dernière analyse terminée : %w", err)
	}
	if len(passes) == 0 {
		return nil, nil
	}
	return passes[0], nil
}

// groupesDuRang lit une page de groupes, un de plus que nécessaire.
//
// L'agrégat se construit en SQL — GROUP BY, somme des occurrences, compte des
// formes — et non en Go : ramener toutes les formes pour les sommer en mémoire
// tiendrait le corpus entier en RAM, ce que le Raspberry Pi de la promesse ne
// fait pas.
//
// MAX() sur la catégorie et sur le drapeau de résolution, et ce n'est pas un
// choix arbitraire : les deux sont fonction de la clé du groupe — food porte
// le nom canonique de l'entrée quand elle se résout, et cette entrée porte une
// seule catégorie. Toutes les lignes d'un groupe donnent donc la même valeur,
// et MAX est simplement la façon de la faire traverser un GROUP BY.
//
// Les valeurs passent par dbx.Params, jamais par concaténation.
func groupesDuRang(app core.App, analyse string, criteres criteresDesAliments) ([]groupeDAliment, error) {
	requete := app.DB().
		Select(
			"analyses_formes.food AS aliment",
			"MAX(analyses_formes.category) AS categorie",
			"MAX(analyses_formes.resolved) AS resolu",
			"SUM(analyses_formes.occurrences) AS occurrences_totales",
			"COUNT(*) AS formes_avalees",
		).
		From("analyses_formes").
		Where(dbx.NewExp("analyses_formes.analysis = {:analyse}", dbx.Params{"analyse": analyse})).
		GroupBy("analyses_formes.food")

	if !criteres.Tout {
		// Ce qui rend un groupe « tranché » : sa clé porte au moins une ligne
		// dans analyses_annotations. C'est la forme la plus simple, et c'est
		// PATA-127 qui dira ensuite quel genre d'annotation compte.
		//
		// Dans le WHERE et non dans le HAVING : le critère ne dépend que de
		// food, qui est la clé du groupe, et le filtrer ligne à ligne évite de
		// bâtir des groupes qu'on jette ensuite.
		//
		// Le « food != '' » n'est pas un ornement : une annotation de forme —
		// celles que PATA-127 posera sur une ligne plutôt que sur un groupe —
		// laisse son propre champ food vide, et sans ce terme elle trancherait
		// le groupe des aliments vides que le signal aliment_vide désigne.
		requete = requete.AndWhere(dbx.NewExp(
			`NOT EXISTS (SELECT 1 FROM analyses_annotations
			             WHERE analyses_annotations.food = analyses_formes.food
			               AND analyses_annotations.food != '')`))

		// Le signal filtre : on ne garde que les groupes portant au moins un
		// signal — ce qui évacue sel et poivre tout seul. Dans le HAVING,
		// celui-là : un groupe compte dès qu'*une* de ses formes est signalée.
		requete = requete.Having(dbx.NewExp(
			"MAX(json_array_length(analyses_formes.signals)) > 0"))
	}

	groupes := []groupeDAliment{}
	err := requete.
		OrderBy(ordreDesGroupes(criteres.Tri)...).
		Limit(int64(parPage + 1)).
		Offset(int64((criteres.Page - 1) * parPage)).
		All(&groupes)
	return groupes, err
}

// ordreDesGroupes rend les colonnes de tri, dans l'ordre.
//
// Le premier terme est le même pour les quatre ordres, et il n'est pas
// décoratif : les aliments non résolus forment leur propre tas. Un non résolu
// vu 4 000 fois ne passe donc pas devant un résolu vu 40 — ce sont deux
// populations qu'on ne relit pas de la même façon, l'une pour juger une entrée
// du lexique, l'autre pour en écrire une qui manque.
//
// Le dernier terme n'est pas décoratif non plus : sans lui, deux groupes à
// égalité sur le critère demandé s'ordonneraient au gré de la base, et la même
// ligne pourrait apparaître sur deux pages successives — ou sur aucune.
func ordreDesGroupes(tri string) []string {
	const tas, cle = "resolu DESC", "aliment ASC"

	switch tri {
	case triParDispersion:
		return []string{tas, "formes_avalees DESC", cle}
	case triParCategorie:
		return []string{tas, "categorie ASC", cle}
	case triParRarete:
		return []string{tas, "occurrences_totales ASC", cle}
	default:
		// Le signal a filtré, la fréquence classe : un groupe suspect vu
		// 4 000 fois coûte 4 000 fiches fausses.
		return []string{tas, "occurrences_totales DESC", cle}
	}
}

// poseLesSignaux remplit les signaux des groupes de la page, et d'eux seuls.
//
// Une seconde requête plutôt qu'une colonne de plus dans l'agrégat : l'union
// des signaux d'un groupe demande de déplier la colonne JSON par json_each, ce
// qui multiplie les lignes et fausserait du même coup la somme des occurrences
// et le compte des formes. Une sous-requête corrélée l'éviterait, mais elle
// serait évaluée pour chaque groupe du corpus, avant la limite — donc des
// milliers de fois pour en afficher vingt-quatre.
//
// Bornée aux clés de la page, elle reste petite quel que soit le corpus. Et
// c'est encore du SQL qui agrège : le DISTINCT est celui du GROUP BY.
func poseLesSignaux(app core.App, analyse string, groupes []groupeDAliment) error {
	if len(groupes) == 0 {
		return nil
	}

	aliments := make([]any, 0, len(groupes))
	for _, groupe := range groupes {
		aliments = append(aliments, groupe.Aliment)
	}

	var lignes []struct {
		Aliment string `db:"aliment"`
		Signal  string `db:"signal"`
	}
	err := app.DB().
		Select("analyses_formes.food AS aliment", "signal.value AS signal").
		From("analyses_formes", "json_each(analyses_formes.signals) signal").
		Where(dbx.NewExp("analyses_formes.analysis = {:analyse}", dbx.Params{"analyse": analyse})).
		AndWhere(dbx.In("analyses_formes.food", aliments...)).
		GroupBy("analyses_formes.food", "signal.value").
		OrderBy("analyses_formes.food", "signal.value").
		All(&lignes)
	if err != nil {
		return fmt.Errorf("signaux des groupes : %w", err)
	}

	parAliment := make(map[string][]string, len(groupes))
	for _, ligne := range lignes {
		parAliment[ligne.Aliment] = append(parAliment[ligne.Aliment], signalAffiche(ligne.Signal))
	}
	for i := range groupes {
		groupes[i].Signaux = parAliment[groupes[i].Aliment]
	}
	return nil
}

// lesDeuxTas sépare les groupes résolus de ceux qui ne le sont pas.
//
// Les deux tranches sont contiguës dans le résultat — c'est ce que le premier
// terme de l'ordre garantit —, et la séparation est faite ici plutôt que dans
// le gabarit : c'est une donnée de la page, pas une question de mise en forme.
func lesDeuxTas(groupes []groupeDAliment) (resolus, nonResolus []groupeDAliment) {
	for _, groupe := range groupes {
		if groupe.Resolu {
			resolus = append(resolus, groupe)
		} else {
			nonResolus = append(nonResolus, groupe)
		}
	}
	return resolus, nonResolus
}

// barreDesTris rend les quatre ordres atteignables, celui en cours marqué.
func barreDesTris(criteres criteresDesAliments) []lienDeTri {
	barre := make([]lienDeTri, 0, 4)
	for _, tri := range []struct{ cle, nom string }{
		{"", "Les plus coûteux"},
		{triParDispersion, "Dispersion"},
		{triParCategorie, "Par catégorie"},
		{triParRarete, "Rareté"},
	} {
		barre = append(barre, lienDeTri{
			Nom:     tri.nom,
			Adresse: criteres.lienDuTri(tri.cle),
			Actif:   criteres.Tri == tri.cle,
		})
	}
	return barre
}

// signalAffiche dit un signal en français.
//
// Jamais la valeur enregistrée, pour la raison déjà écrite sur statutAffiche :
// « unite_repetee » est une clé de schéma, pas un mot qu'on montre. Une clé
// inconnue — un signal ajouté par du code plus récent que cet écran — ressort
// telle quelle plutôt que disparaître : une ligne signalée doit se voir même
// mal nommée.
func signalAffiche(signal string) string {
	switch signal {
	case SignalMotsPerdus:
		return "mots perdus"
	case SignalAlimentVide:
		return "aliment vide"
	case SignalUniteRepetee:
		return "unité répétée"
	case SignalNonResolu:
		return "non résolu"
	case SignalMotsReordonnes:
		return "mots réordonnés"
	default:
		return signal
	}
}
