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

// L'établi : la page détail, parcourue et cherchée.
//
// Elle répond à « d'où sort cet aliment bizarre ? ». Depuis un groupe de la
// vue agrégée, on descend sur les formes qui l'ont produit, chacune avec la
// lecture du parser champ à champ ; depuis une forme, on remonte à l'entrée
// canonique qui l'a attrapée. C'est ce va-et-vient qui fait qu'on comprend une
// erreur au lieu de la constater.
//
// Deux écrans, et c'est délibéré : une page qui se parcourt — la liste, avec
// sa recherche et sa pagination — et la fiche d'une forme, où la provenance se
// relit. La séparation n'est pas cosmétique, c'est elle qui tient la promesse
// de coût : la provenance interroge ingredients.raw, qui n'est pas indexé
// (le seul index de la collection est (recipe, position)), et une requête par
// ligne de liste coûterait une lecture complète de la table par forme
// affichée. Elle n'est donc émise que pour la forme qu'on regarde.
//
// Cette page ne lit que ce que l'analyse a écrit, plus le carnet, et elle
// n'ajoute aucune route en écriture : l'établi lit le carnet, il ne le modifie
// pas.

// cheminDesFormesDeLEtabli accroche la liste sous le préfixe de l'établi, posé
// par la page de lancement. Aucun autre préfixe n'est inventé ici.
const cheminDesFormesDeLEtabli = cheminDeLEtabli + "/formes"

// motifDeLaFicheDUneForme est le motif de route de la fiche. La fiche se
// désigne par l'identifiant de la forme, et non par sa ligne brute : deux
// passes peuvent porter la même ligne, et une ligne brute dans un chemin
// d'URL serait à échapper des deux côtés.
const motifDeLaFicheDUneForme = cheminDesFormesDeLEtabli + "/{id}"

// cheminDeLaFicheDUneForme rend l'adresse de la fiche d'une forme.
//
// Une fonction plutôt qu'une concaténation recopiée : c'est elle que les
// gabarits reçoivent, et c'est elle que les tests suivent.
func cheminDeLaFicheDUneForme(id string) string {
	return cheminDesFormesDeLEtabli + "/" + url.PathEscape(id)
}

// Les paramètres de la chaîne de requête propres à cet écran. La passe et la
// page reprennent ceux de la vue agrégée : ce sont les mêmes critères, et deux
// noms pour la même chose finiraient par diverger.
const (
	// parametreDeLAliment porte la clé du groupe. Sa présence filtre, et non
	// sa valeur : le groupe des lignes dont aucun aliment n'a été lu a la
	// chaîne vide pour clé, et « aliment= » doit l'atteindre là où l'absence
	// du paramètre ouvre la liste entière.
	parametreDeLAliment = "aliment"

	// parametreDuTerme est celui de la recherche du carnet, et volontairement
	// le même : « q » est ce qu'on tape dans une URL sans y penser.
	parametreDuTerme = "q"
)

// provenancesAffichees borne la provenance d'une forme.
//
// Une ligne banale — « 1 pincée de sel » — est portée par tout le carnet, et
// la fiche n'a pas à en déplier des milliers pour répondre à « d'où sort
// cette ligne ». La borne est dite à l'écran quand elle mord : une liste
// tronquée en silence se lit comme une liste complète.
const provenancesAffichees = 10

// criteresDesFormes porte ce que la chaîne de requête dit de la liste.
//
// Le patron de criteresDesAliments, et pour la même raison : un seul type, un
// seul lecteur, et des liens qui se réécrivent sans que la page ait à
// recomposer une URL à chaque fois.
type criteresDesFormes struct {
	// Analyse est l'identifiant de la passe demandée, ou "" pour la dernière
	// terminée. Résolu par laPasseAffichee, comme la vue agrégée.
	Analyse string

	// Aliment est la clé du groupe, et Filtre dit si le paramètre était là.
	// Les deux, parce que la chaîne vide est une clé comme une autre.
	Aliment string
	Filtre  bool

	Terme string
	Page  int
}

// lisLesCriteresDesFormes est le seul endroit où la chaîne de requête est lue.
//
// Un paramètre malmené ne produit pas d'erreur : une page non numérique, nulle
// ou négative retombe sur la première. Refuser vaudrait une 500 pour un lien
// mal recopié — c'est déjà la règle de lisLesCriteres et de
// lisLesCriteresDesAliments, et cet écran n'en invente pas une autre.
func lisLesCriteresDesFormes(r *http.Request) criteresDesFormes {
	requete := r.URL.Query()

	page, err := strconv.Atoi(requete.Get(parametreDeLaPage))
	if err != nil || page < 1 {
		page = 1
	}
	// La même borne que les deux autres listes, et pour la même raison : le
	// décalage se calcule par (page-1)*parPage, et un nombre démesuré le
	// ferait déborder en négatif.
	if page > pageMax {
		page = pageMax
	}

	return criteresDesFormes{
		Analyse: strings.TrimSpace(requete.Get(parametreDeLAnalyse)),
		Aliment: requete.Get(parametreDeLAliment),
		Filtre:  requete.Has(parametreDeLAliment),
		Terme:   strings.TrimSpace(requete.Get(parametreDuTerme)),
		Page:    page,
	}
}

// lien rend l'adresse de la liste pour ces critères, page comprise.
//
// La page 1 ne s'écrit pas : deux adresses pour la même chose finiraient par
// diverger. Le filtre, lui, s'écrit même vide — c'est sa présence qui compte.
func (c criteresDesFormes) lien(page int) string {
	valeurs := url.Values{}
	if c.Analyse != "" {
		valeurs.Set(parametreDeLAnalyse, c.Analyse)
	}
	if c.Filtre {
		valeurs.Set(parametreDeLAliment, c.Aliment)
	}
	if c.Terme != "" {
		valeurs.Set(parametreDuTerme, c.Terme)
	}
	if page > 1 {
		valeurs.Set(parametreDeLaPage, strconv.Itoa(page))
	}

	if len(valeurs) == 0 {
		return cheminDesFormesDeLEtabli
	}
	return cheminDesFormesDeLEtabli + "?" + valeurs.Encode()
}

// lienDesFormesDuGroupe rend l'adresse des formes d'un groupe, dans la passe
// donnée.
//
// C'est le lien que la vue agrégée porte sur chacune de ses lignes — « depuis
// le groupe, au détail » — et celui que la fiche d'une forme remonte. Écrit
// une fois, ici, plutôt que composé des deux côtés.
//
// La passe y est toujours écrite, même quand c'est la dernière terminée :
// descendre d'un groupe ne doit pas pouvoir changer d'analyse en chemin parce
// qu'une passe s'est terminée entre l'affichage et le clic.
func lienDesFormesDuGroupe(analyse, aliment string) string {
	return criteresDesFormes{Analyse: analyse, Aliment: aliment, Filtre: true}.lien(1)
}

// lectureAffichee est la lecture du parser telle que l'écran la montre.
//
// Les champs de lectureDUneForme, mis en chaînes : la quantité est un
// *float64 que le gabarit ne saurait pas rendre sans zéro inutile, et un
// gabarit n'est pas l'endroit où l'on met en forme un nombre.
type lectureAffichee struct {
	Quantite  string
	Unite     string
	Partitif  string
	Aliment   string
	Note      string
	Optionnel bool
}

// lectureAffichable met une lecture en forme pour l'écran.
func lectureAffichable(lu lectureDUneForme) lectureAffichee {
	quantite := ""
	if lu.Quantite != nil {
		quantite = quantiteLisible(*lu.Quantite)
	}
	return lectureAffichee{
		Quantite:  quantite,
		Unite:     lu.Unite,
		Partitif:  lu.Partitif,
		Aliment:   lu.Aliment,
		Note:      lu.Note,
		Optionnel: lu.Optionnel,
	}
}

// formeListee est ce qu'une ligne de la liste montre.
type formeListee struct {
	Lien         string
	Brut         string
	Occurrences  int
	Lecture      lectureAffichee
	Signaux      []string
	Groupe       string
	LienDuGroupe string
}

// donneesFormes est ce que le gabarit de la liste reçoit.
type donneesFormes struct {
	donneesPage

	// SansAnalyse ouvre le seul autre état de la page : tant qu'aucune passe
	// n'est terminée, elle le dit et renvoie au lancement plutôt que
	// d'afficher une liste vide.
	SansAnalyse bool

	// Les trois adresses que le gabarit ne recompose pas : le lancement, la
	// vue agrégée, et la liste nue — qui est à la fois l'action du formulaire
	// de recherche et le retour depuis une fiche introuvable.
	LienDuLancement    string
	LienDeLaVueAgregee string
	LienDesFormes      string

	// SansFiltre est la même liste, le groupe retiré : « toutes les formes de
	// la passe ». Vide quand aucun filtre n'est posé.
	SansFiltre string

	// Date situe la passe affichée : la vue porte sur une analyse, et laquelle
	// se lit sur l'écran plutôt que dans l'URL.
	Date string

	// Les champs cachés du formulaire de recherche : la passe et le groupe
	// partent avec le terme, sinon chercher annulerait le filtre.
	Analyse string
	Aliment string
	Filtre  bool

	Terme  string
	Formes []formeListee

	Precedente string
	Suivante   string
}

// origineDeLaForme est une recette du carnet qui porte cette ligne brute.
type origineDeLaForme struct {
	Id    string `db:"id"`
	Titre string `db:"titre"`
}

// donneesForme est ce que le gabarit de la fiche reçoit.
type donneesForme struct {
	donneesPage

	Date        string
	Brut        string
	Occurrences int
	Motif       string
	Categorie   string
	Resolu      bool
	Lecture     lectureAffichee
	Signaux     []string

	// Aliment est la clé du groupe, LienDuGroupe y remonte, et Autres dit
	// combien de formes s'y rangent en plus de celle-ci.
	Aliment      string
	LienDuGroupe string
	Autres       int

	LienDeLaListe string

	// Annotation porte la zone de verdict : ce qui a déjà été dit de cette
	// ligne, et le formulaire pour en dire plus. Le gabarit s'ouvre sur un
	// {{with}}, comme la fiche recette le fait de ses blocs.
	Annotation *blocDAnnotationDUneForme

	// AvecProvenance ouvre le bloc, et il reste fermé sur un corpus fourni :
	// le fichier n'est pas conservé (PATA-124), la passe n'a donc aucune
	// provenance à relire. Un bloc vide se lirait comme une panne.
	AvecProvenance   bool
	Provenance       []origineDeLaForme
	ProvenanceBornee bool
	Borne            int
}

// brancheLesFormes pose les deux écrans de détail, en lecture seule et
// derrière le droit d'entrer dans l'établi.
//
// Deux GET et rien d'autre : la seule écriture de l'établi a ses propres
// routes (annotations.go), et la fiche ne fait que porter le formulaire qui
// les atteint. La garde est celle des autres écrans, pas une seconde :
// exigeUnCurateur, qui renvoie le visiteur se connecter et refuse le compte
// connecté sans le droit.
func brancheLesFormes(routeur *router.Router[*core.RequestEvent]) {
	routeur.GET(cheminDesFormesDeLEtabli, pageDesFormes).Bind(exigeUnCurateur())
	routeur.GET(motifDeLaFicheDUneForme, pageDeLaForme).Bind(exigeUnCurateur())
}

// pageDesFormes rend la liste des formes distinctes de la passe affichée.
func pageDesFormes(e *core.RequestEvent) error {
	criteres := lisLesCriteresDesFormes(e.Request)

	passe, err := laPasseAffichee(e.App, criteres.Analyse)
	if err != nil {
		return err
	}
	if passe == nil {
		return rendre(e, "etabli-formes.html", "etabli-formes-corps.html", &donneesFormes{
			donneesPage:        donneesPage{Titre: "Les formes lues — Patachoo"},
			SansAnalyse:        true,
			LienDuLancement:    cheminDeLEtabli,
			LienDeLaVueAgregee: cheminDesAlimentsDeLEtabli,
			LienDesFormes:      cheminDesFormesDeLEtabli,
		})
	}

	formes, err := formesDuRang(e.App, passe.Id, criteres)
	if err != nil {
		return err
	}

	// Une forme de plus que la page, comme recettesDuRang et groupesDuRang :
	// c'est elle, et elle seule, qui dit s'il existe une page suivante — sans
	// second décompte, donc sans un COUNT de plus sur le corpus entier.
	suivante := len(formes) > parPage
	if suivante {
		formes = formes[:parPage]
	}

	listees, err := formesListees(passe.Id, formes)
	if err != nil {
		return err
	}

	donnees := &donneesFormes{
		donneesPage:        donneesPage{Titre: "Les formes lues — Patachoo"},
		LienDuLancement:    cheminDeLEtabli,
		LienDeLaVueAgregee: cheminDesAlimentsDeLEtabli,
		LienDesFormes:      cheminDesFormesDeLEtabli,
		Date:               dateEnFrancais(passe.GetDateTime("created")),
		Analyse:            criteres.Analyse,
		Aliment:            criteres.Aliment,
		Filtre:             criteres.Filtre,
		Terme:              criteres.Terme,
		Formes:             listees,
	}
	if criteres.Filtre {
		donnees.SansFiltre = criteresDesFormes{Analyse: criteres.Analyse, Terme: criteres.Terme}.lien(1)
	}

	if criteres.Page > 1 {
		donnees.Precedente = criteres.lien(criteres.Page - 1)
	}
	if suivante {
		donnees.Suivante = criteres.lien(criteres.Page + 1)
	}

	return rendre(e, "etabli-formes.html", "etabli-formes-corps.html", donnees)
}

// formesDuRang lit une page de formes, une de plus que nécessaire.
//
// Une requête construite, et non FindRecordsByFilter : MATCH ne s'exprime pas
// dans le filtre en chaîne de PocketBase. C'est le patron de recettesDuRang, y
// compris pour la jointure sur la table virtuelle — l'index porte une ligne
// par forme, il n'y a donc rien à dédupliquer.
//
// Le repli des accents et la recherche par préfixe viennent du tokeniseur
// « unicode61 remove_diacritics 2 » posé par PATA-122 et de la citation de
// motifDeRecherche : il n'y a rien à normaliser côté Go.
//
// L'ordre par occurrences décroissantes est celui de la vue agrégée, et pour
// la même raison : une forme mal lue vue 4 000 fois coûte 4 000 fiches
// fausses. Le second terme n'est pas décoratif — sans lui, deux formes à
// égalité s'ordonneraient au gré de la base, et la même ligne pourrait
// apparaître sur deux pages successives, ou sur aucune.
func formesDuRang(app core.App, analyse string, criteres criteresDesFormes) ([]*core.Record, error) {
	requete := app.RecordQuery("analyses_formes").
		AndWhere(dbx.NewExp("analyses_formes.analysis = {:analyse}", dbx.Params{"analyse": analyse}))

	if criteres.Filtre {
		// La valeur passe par dbx.Params, jamais par concaténation. La chaîne
		// vide est une clé comme une autre : c'est le groupe des lignes dont
		// aucun aliment n'a été lu, celui que le signal aliment_vide désigne.
		requete = requete.AndWhere(dbx.NewExp(
			"analyses_formes.food = {:aliment}", dbx.Params{"aliment": criteres.Aliment}))
	}

	if motif := motifDeRecherche(criteres.Terme); motif != "" {
		requete = requete.
			InnerJoin("analyses_formes_fts",
				dbx.NewExp("analyses_formes_fts.form_id = analyses_formes.id")).
			AndWhere(dbx.NewExp("analyses_formes_fts MATCH {:q}", dbx.Params{"q": motif}))
	}

	formes := []*core.Record{}
	err := requete.
		OrderBy("analyses_formes.occurrences DESC", "analyses_formes.raw ASC").
		Limit(parPage + 1).
		Offset(int64((criteres.Page - 1) * parPage)).
		All(&formes)
	return formes, err
}

// formesListees met les formes d'une page en forme pour le gabarit.
//
// Aucune requête ici, et c'est le point : la lecture et les signaux sont deux
// colonnes JSON de la forme elle-même, et le lien du groupe se compose. La
// provenance, elle, n'est pas de ce voyage — voir provenanceDeLaForme.
func formesListees(analyse string, formes []*core.Record) ([]formeListee, error) {
	listees := make([]formeListee, 0, len(formes))
	for _, forme := range formes {
		lecture, signaux, err := lectureEtSignauxDe(forme)
		if err != nil {
			return nil, err
		}

		aliment := forme.GetString("food")
		listees = append(listees, formeListee{
			Lien:         cheminDeLaFicheDUneForme(forme.Id),
			Brut:         forme.GetString("raw"),
			Occurrences:  forme.GetInt("occurrences"),
			Lecture:      lecture,
			Signaux:      signaux,
			Groupe:       aliment,
			LienDuGroupe: lienDesFormesDuGroupe(analyse, aliment),
		})
	}
	return listees, nil
}

// lectureEtSignauxDe déplie les deux colonnes JSON d'une forme.
//
// Les signaux sont dits en français par signalAffiche, comme la vue agrégée le
// fait déjà : « unite_repetee » est une clé de schéma, pas un mot qu'on montre.
func lectureEtSignauxDe(forme *core.Record) (lectureAffichee, []string, error) {
	var lu lectureDUneForme
	if err := forme.UnmarshalJSONField("reading", &lu); err != nil {
		return lectureAffichee{}, nil, fmt.Errorf("lecture de la forme %q : %w", forme.Id, err)
	}

	var bruts []string
	if err := forme.UnmarshalJSONField("signals", &bruts); err != nil {
		return lectureAffichee{}, nil, fmt.Errorf("signaux de la forme %q : %w", forme.Id, err)
	}

	signaux := make([]string, 0, len(bruts))
	for _, signal := range bruts {
		signaux = append(signaux, signalAffiche(signal))
	}
	return lectureAffichable(lu), signaux, nil
}

// pageDeLaForme rend la fiche d'une forme : sa lecture, son groupe, et d'où
// elle vient.
func pageDeLaForme(e *core.RequestEvent) error {
	forme, err := e.App.FindRecordById("analyses_formes", e.Request.PathValue("id"))
	if err != nil {
		// L'erreur est avalée à dessein : « cette forme n'existe pas » est le
		// cas normal d'une URL tapée de travers, ou d'un lien vers une passe
		// supprimée depuis. Une page lisible, et non une 500.
		return pageDeLaFormeIntrouvable(e)
	}

	passe, err := e.App.FindRecordById("analyses", forme.GetString("analysis"))
	if err != nil {
		return fmt.Errorf("analyse de la forme %q : %w", forme.Id, err)
	}

	lecture, signaux, err := lectureEtSignauxDe(forme)
	if err != nil {
		return err
	}

	aliment := forme.GetString("food")
	autres, err := autresFormesDuGroupe(e.App, passe.Id, aliment)
	if err != nil {
		return err
	}

	donnees := &donneesForme{
		donneesPage:   donneesPage{Titre: "Une forme lue — Patachoo"},
		Date:          dateEnFrancais(passe.GetDateTime("created")),
		Brut:          forme.GetString("raw"),
		Occurrences:   forme.GetInt("occurrences"),
		Motif:         forme.GetString("pattern"),
		Categorie:     forme.GetString("category"),
		Resolu:        forme.GetBool("resolved"),
		Lecture:       lecture,
		Signaux:       signaux,
		Aliment:       aliment,
		LienDuGroupe:  lienDesFormesDuGroupe(passe.Id, aliment),
		Autres:        autres,
		LienDeLaListe: criteresDesFormes{Analyse: passe.Id}.lien(1),
		Borne:         provenancesAffichees,
	}

	// La zone d'annotation, sur la clé naturelle de la forme : les annotations
	// posées sur cette ligne, quelle que soit la passe qui l'avait lue, et le
	// formulaire pour en poser une de plus.
	donnees.Annotation, err = leBlocDeLaForme(e, passe.Id, donnees.Brut)
	if err != nil {
		return err
	}

	// La provenance ne se relit que sur un corpus venu de la base de
	// l'instance. Un corpus fourni n'en a aucune — pas même un numéro de
	// ligne, le fichier n'étant pas conservé (PATA-124) —, et une égalité sur
	// ingredients.raw y rapprocherait des lignes que rien ne lie : la ligne
	// analysée vient d'ailleurs, et la recette qui porte la même chaîne n'est
	// pas sa source.
	if passe.GetString("source") == sourceInstance {
		donnees.AvecProvenance = true
		donnees.Provenance, donnees.ProvenanceBornee, err = provenanceDeLaForme(e.App, donnees.Brut)
		if err != nil {
			return err
		}
	}

	return rendre(e, "etabli-forme.html", "etabli-forme-corps.html", donnees,
		gabaritsDuBlocDeLaForme...)
}

// pageDeLaFormeIntrouvable répond par une page lisible, et non par une 500 ni
// une page vide. Le patron de pageRecetteIntrouvable.
func pageDeLaFormeIntrouvable(e *core.RequestEvent) error {
	return rendreAvecStatut(e, http.StatusNotFound,
		"etabli-forme-introuvable.html", "etabli-forme-introuvable-corps.html", &donneesFormes{
			donneesPage:        donneesPage{Titre: "Forme introuvable — Patachoo"},
			LienDuLancement:    cheminDeLEtabli,
			LienDeLaVueAgregee: cheminDesAlimentsDeLEtabli,
			LienDesFormes:      cheminDesFormesDeLEtabli,
		})
}

// autresFormesDuGroupe compte les formes du groupe en plus de celle qu'on
// regarde : « rangée sous feuille de laurier, voir les 6 autres formes ».
//
// Un décompte et non une liste : la fiche dit combien il y en a, et le lien
// mène à la liste qui les montre. Il porte sur la colonne food, qui est la clé
// du groupe — la même que celle de la vue agrégée.
func autresFormesDuGroupe(app core.App, analyse, aliment string) (int, error) {
	total, err := app.CountRecords("analyses_formes",
		dbx.HashExp{"analysis": analyse, "food": aliment})
	if err != nil {
		return 0, fmt.Errorf("formes du groupe %q : %w", aliment, err)
	}
	if total < 1 {
		return 0, nil
	}
	return int(total) - 1, nil
}

// provenanceDeLaForme relit les recettes du carnet qui portent cette ligne
// brute, et dit si le résultat a été tronqué.
//
// Rien n'est stocké : l'arbitrage du 20/09/2026 est « on relit la base ».
// L'égalité porte sur ingredients.raw, puis la relation ingredients.recipe
// mène à la fiche. Les deux champs existent depuis le schéma initial.
//
// Une jointure interne, et c'est elle qui écarte les liens morts : une ligne
// dont la recette a disparu porte une relation vide, et serait listée comme
// une recette sans titre menant à une page introuvable.
//
// Le regroupement par recette dit ce que la provenance nomme : des recettes,
// pas des occurrences. Une recette qui porte deux fois la même ligne n'y
// figure qu'une fois.
//
// Une de plus que la borne est demandée : c'est elle, et elle seule, qui dit
// que la liste est tronquée — sans second décompte sur une colonne qui n'est
// pas indexée.
func provenanceDeLaForme(app core.App, brut string) ([]origineDeLaForme, bool, error) {
	origines := []origineDeLaForme{}
	err := app.DB().
		Select("recipes.id AS id", "recipes.title AS titre").
		From("ingredients").
		InnerJoin("recipes", dbx.NewExp("recipes.id = ingredients.recipe")).
		Where(dbx.NewExp("ingredients.raw = {:brut}", dbx.Params{"brut": brut})).
		GroupBy("recipes.id").
		OrderBy("recipes.title ASC", "recipes.id ASC").
		Limit(int64(provenancesAffichees + 1)).
		All(&origines)
	if err != nil {
		return nil, false, fmt.Errorf("provenance de la ligne %q : %w", brut, err)
	}

	bornee := len(origines) > provenancesAffichees
	if bornee {
		origines = origines[:provenancesAffichees]
	}
	return origines, bornee, nil
}
