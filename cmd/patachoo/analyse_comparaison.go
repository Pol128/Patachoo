package main

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/spf13/cobra"
)

// La comparaison de deux lectures du même corpus : ce qui dit si le parser lit
// mieux qu'avant.
//
// Le corpus de l'instance n'a pas de vérité de référence — on ne peut pas
// prouver qu'une ligne donnée est mieux lue qu'avant. Mais on n'en a pas
// besoin : les cinq signaux de relecture se calculent à partir de la seule
// lecture, et « mieux » se lit donc « moins de signaux allumés ». Quatre
// quantités relevées de part et d'autre, plus une règle sans tolérance —
// aucune forme ne passe de zéro signal à au moins un.
//
// Le rapprochement se fait sur raw, qui ne bouge jamais d'une passe à l'autre
// et qu'un index rend unique par passe. Une forme présente d'un seul côté ne
// dit rien du parser, seulement que le corpus a bougé : elle est comptée à
// part, et elle n'entre dans aucun écart — c'est pour ça que les quantités
// portent sur les seules formes communes, et ne redonnent donc pas les totaux
// de analyse resume.

// commandeAnalyseComparer : la sous-commande, rangée à côté de lancer et de
// resume.
//
// Deux formes d'appel, et deux « avant » de nature différente. Entre deux
// passes, tout se compare. Contre la base, l'« avant » est ce qui est déjà
// écrit dans ingredients : c'est la forme qui sert à la mesure historique,
// celle qui dira s'il vaut la peine de reprendre l'existant.
func commandeAnalyseComparer(app core.App, retient func(error) error) *cobra.Command {
	var contreLaBase bool

	comparer := &cobra.Command{
		Use:   "comparer <avant> <après>",
		Short: "Compare deux analyses, ou les colonnes d'ingredients à une analyse.",
		Long: "Le rapprochement se fait sur la ligne brute, qui ne bouge jamais d'une\n" +
			"lecture à l'autre. Une forme présente d'un seul côté est comptée à part :\n" +
			"elle ne dit rien du parser, seulement que le corpus a bougé.\n\n" +
			"Avec --base, l'« avant » n'est pas une analyse mais les colonnes dérivées\n" +
			"de la collection ingredients — lues, jamais écrites. C'est la seule façon\n" +
			"de comparer à une version du parser qui ne se rejoue pas.\n\n" +
			"La sortie est triée et déterministe : elle est faite pour être redirigée\n" +
			"dans un fichier et comparée d'une exécution à l'autre.",
		Args: func(_ *cobra.Command, args []string) error {
			if contreLaBase {
				if len(args) != 1 {
					return fmt.Errorf("--base compare les colonnes d'ingredients à une seule analyse : %d argument(s) donné(s)", len(args))
				}
				return nil
			}
			if len(args) != 2 {
				return fmt.Errorf("deux analyses à comparer attendues, %d argument(s) donné(s) — ou --base pour comparer aux colonnes d'ingredients", len(args))
			}
			return nil
		},
		// Une erreur d'exécution n'est pas une erreur d'usage : afficher l'aide
		// complète par-dessus le message noierait ce dernier.
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return retient(compareLesLectures(app, cmd.OutOrStdout(), contreLaBase, args))
		},
	}
	comparer.Flags().BoolVar(&contreLaBase, "base", false,
		"compare les colonnes d'ingredients à l'analyse donnée, au lieu de deux analyses")
	return comparer
}

// compareLesLectures monte les deux côtés et écrit le compte rendu.
func compareLesLectures(app core.App, sortie io.Writer, contreLaBase bool, args []string) error {
	if contreLaBase {
		apres, err := coteDeLAnalyse(app, args[0])
		if err != nil {
			return err
		}
		avant, err := coteDeLaBase(app)
		if err != nil {
			return err
		}
		return ecritLaComparaison(sortie, compare(avant, apres, perimetreDeLaBase))
	}

	avant, err := coteDeLAnalyse(app, args[0])
	if err != nil {
		return err
	}
	apres, err := coteDeLAnalyse(app, args[1])
	if err != nil {
		return err
	}
	return ecritLaComparaison(sortie, compare(avant, apres, perimetreDeDeuxPasses))
}

// coteDeLAnalyse cherche la passe que porte un identifiant, puis en lit les
// formes.
func coteDeLAnalyse(app core.App, id string) (coteCompare, error) {
	passe, err := trouveLaPasse(app, id)
	if err != nil {
		return coteCompare{}, err
	}
	return coteDUnePasse(app, passe)
}

// motifAbsent est ce que la comparaison compte pour une forme dont le parser
// n'a pas nommé la règle. Une ligne nommée plutôt qu'une ligne vide, comme
// categorieAbsente.
const motifAbsent = "sans motif"

// perimetreDesChamps dit ce qui entre dans la comparaison. Les deux formes de
// la commande ne comparent pas la même chose : deux passes parlent le même
// vocabulaire, la base n'en porte qu'une partie.
type perimetreDesChamps struct {
	unite    bool
	partitif bool
	// signaux dit si les deux côtés savent allumer un signal et nommer un
	// motif : sans eux, il n'y a ni quantité ni régression à mesurer.
	signaux bool
	// zeroVautAbsente replie un quantity à zéro sur « pas de quantité ». Le
	// champ nombre de PocketBase n'est pas nullable et ingredients.pose y écrit
	// donc zéro là où la lecture n'a rien : sans ce repli, toute ligne sans
	// quantité ressortirait changée.
	zeroVautAbsente bool
	// pourquoi explique, dans la sortie, ce qui est laissé de côté. Un champ
	// exclu sans raison écrite passerait pour un oubli.
	pourquoi string
}

// perimetreDeDeuxPasses : tout se compare. Les deux côtés sortent de la même
// colonne reading, écrite par le même code.
var perimetreDeDeuxPasses = perimetreDesChamps{unite: true, partitif: true, signaux: true}

// perimetreDeLaBase : ce que les colonnes d'ingredients savent dire, et rien de
// plus.
var perimetreDeLaBase = perimetreDesChamps{
	zeroVautAbsente: true,
	pourquoi: "  unit porte l'abréviation canonique en base et le texte de la ligne dans une passe — les\n" +
		"  deux sont voulus, et comparer ce champ dirait « changé » sur toute ligne bien lue.\n" +
		"  partitive, les signaux et le motif n'existent pas dans ingredients : les recalculer avec\n" +
		"  le lexique du jour rendrait un « avant » qui n'a jamais existé.",
}

// horsComparaison nomme les champs que ce périmètre laisse de côté, dans
// l'ordre de la lecture.
func (p perimetreDesChamps) horsComparaison() []string {
	var hors []string
	if !p.unite {
		hors = append(hors, "unit")
	}
	if !p.partitif {
		hors = append(hors, "partitive")
	}
	return hors
}

// projette rend la lecture telle qu'elle sera comparée : les champs hors
// périmètre vidés, la quantité repliée si le périmètre le demande.
func (p perimetreDesChamps) projette(lue lectureDUneForme) lectureDUneForme {
	if !p.unite {
		lue.Unite = ""
	}
	if !p.partitif {
		lue.Partitif = ""
	}
	if p.zeroVautAbsente && lue.Quantite != nil && *lue.Quantite == 0 {
		lue.Quantite = nil
	}
	return lue
}

// formeComparee est une forme telle que la comparaison la lit.
type formeComparee struct {
	lecture lectureDUneForme
	signaux []string
	resolu  bool
	motif   string
}

// coteCompare est un côté de la comparaison : d'où il vient, et ses formes par
// ligne brute.
type coteCompare struct {
	// origine se lit dans la sortie. C'est ce qui permet de relire un compte
	// rendu six mois plus tard sans se demander de quoi il parle.
	origine string
	formes  map[string]formeComparee
}

// coteDUnePasse lit les formes d'une analyse.
//
// En mémoire, comme resumeDe : les signaux sont un tableau JSON, et une
// commande lancée à la main ne remarquera pas le temps que coûtent cinquante
// mille formes.
func coteDUnePasse(app core.App, passe *core.Record) (coteCompare, error) {
	formes, err := app.FindAllRecords("analyses_formes", dbx.HashExp{"analysis": passe.Id})
	if err != nil {
		return coteCompare{}, fmt.Errorf("lecture des formes de l'analyse %q : %w", passe.Id, err)
	}

	cote := coteCompare{
		origine: "analyse " + passe.Id,
		formes:  make(map[string]formeComparee, len(formes)),
	}
	for _, forme := range formes {
		var lue lectureDUneForme
		if err := forme.UnmarshalJSONField("reading", &lue); err != nil {
			return coteCompare{}, fmt.Errorf("lecture de la forme %q : %w", forme.Id, err)
		}
		var signaux []string
		if err := forme.UnmarshalJSONField("signals", &signaux); err != nil {
			return coteCompare{}, fmt.Errorf("signaux de la forme %q : %w", forme.Id, err)
		}
		motif := strings.TrimSpace(forme.GetString("pattern"))
		if motif == "" {
			motif = motifAbsent
		}
		cote.formes[forme.GetString("raw")] = formeComparee{
			lecture: lue,
			signaux: signaux,
			resolu:  forme.GetBool("resolved"),
			motif:   motif,
		}
	}
	return cote, nil
}

// coteDeLaBase lit les cinq champs dérivés des lignes de l'instance.
//
// C'est le premier « avant », et il n'est pas une passe : Patachoo n'embarque
// qu'un parser à la fois, et la version qui a écrit ces colonnes ne se rejoue
// pas. La colonne reading d'une passe emploie exprès les mêmes clés, ce qui
// fait de la comparaison une comparaison champ à champ, sans traduction.
//
// Dédoublonné sur raw, comme une passe : la même ligne peut être écrite dans
// deux recettes. Deux lignes de même brut portent la même lecture par
// construction — c'est le hook qui les remplit —, et laquelle des deux est
// retenue ne se voit donc pas. Sauf là où quelqu'un a corrigé un champ à la
// main : la première rencontrée l'emporte alors, dans un ordre stable d'une
// exécution à l'autre mais qui n'est pas celui de la saisie, les identifiants
// de PocketBase étant tirés au hasard. Aucun test ne fixe ce choix, faute de
// pouvoir le rendre reproductible.
//
// Une lecture, et rien d'autre : la comparaison n'écrit ni dans ingredients ni
// dans recipes.
func coteDeLaBase(app core.App) (coteCompare, error) {
	lignes, err := app.FindRecordsByFilter("ingredients", "id != ''", "id", 0, 0)
	if err != nil {
		return coteCompare{}, fmt.Errorf("lecture des lignes de l'instance : %w", err)
	}

	cote := coteCompare{
		origine: "les colonnes d'ingredients",
		formes:  make(map[string]formeComparee, len(lignes)),
	}
	for _, ligne := range lignes {
		// Le même repli que litLeCorpus : la forme est la ligne brute, espaces
		// de bord retirés, sans quoi les deux côtés ne se rejoindraient pas.
		brut := strings.TrimSpace(ligne.GetString("raw"))
		if brut == "" {
			continue
		}
		if _, vue := cote.formes[brut]; vue {
			continue
		}
		quantite := ligne.GetFloat("quantity")
		cote.formes[brut] = formeComparee{
			lecture: lectureDUneForme{
				Quantite:  &quantite,
				Unite:     ligne.GetString("unit"),
				Aliment:   ligne.GetString("food"),
				Note:      ligne.GetString("note"),
				Optionnel: ligne.GetBool("optional"),
			},
		}
	}
	return cote, nil
}

// quantitesDUnCote porte les quantités d'un côté, sur les seules formes
// communes.
type quantitesDUnCote struct {
	formes     int
	sansSignal int
	resolus    int
	parSignal  map[string]int
	parMotif   map[string]int
}

// comparaison est ce que la commande rend.
type comparaison struct {
	avant, apres coteCompare
	perimetre    perimetreDesChamps

	communes       []string
	seulementAvant []string
	seulementApres []string

	quantitesAvant quantitesDUnCote
	quantitesApres quantitesDUnCote

	identiques int
	// canonisations compte les formes dont l'aliment est réécrit avec la forme
	// canonique du lexique. MOTEUR-5 le fait sur toute ligne qui se résout :
	// la moitié du corpus change sans qu'aucune erreur soit réparée, et un
	// décompte de formes changées qui l'inclurait serait ininterprétable.
	// Comptée, nommée, et tenue hors du verdict — ce qui oblige la famille à
	// ne prendre que ce qu'elle nomme, cf. estUneCanonisation.
	canonisations int
	regressions   []ecartDeForme
	aRelire       []ecartDeForme
}

// ecartDeForme est une forme qui a changé, avec ses deux lectures. Sans vérité
// de référence, une régression ne se relit pas autrement.
type ecartDeForme struct {
	brut         string
	avant, apres lectureComparee
}

// lectureComparee est ce qu'un écart montre d'un côté : la lecture telle
// qu'elle a été comparée — champs hors périmètre vidés —, et les signaux quand
// le côté en porte.
//
// Les clés sont celles des colonnes : quantity, unit, partitive, food, note,
// optional, signals. Un pointeur pour les signaux, et non une tranche : la clé
// disparaît quand le côté n'en porte pas, là où un tableau vide voudrait dire
// « aucun signal allumé ».
type lectureComparee struct {
	lectureDUneForme
	Signaux *[]string `json:"signals,omitempty"`
}

// compare rapproche les deux côtés sur raw.
//
// Fonction pure : ni base, ni horloge, ni sortie. C'est ce qui permet
// d'éprouver les familles sur des écarts choisis.
func compare(avant, apres coteCompare, perimetre perimetreDesChamps) comparaison {
	c := comparaison{avant: avant, apres: apres, perimetre: perimetre}

	for _, brut := range triesDesFormes(avant.formes) {
		if _, commune := apres.formes[brut]; commune {
			c.communes = append(c.communes, brut)
		} else {
			c.seulementAvant = append(c.seulementAvant, brut)
		}
	}
	for _, brut := range triesDesFormes(apres.formes) {
		if _, commune := avant.formes[brut]; !commune {
			c.seulementApres = append(c.seulementApres, brut)
		}
	}

	c.quantitesAvant = quantitesDe(avant, c.communes)
	c.quantitesApres = quantitesDe(apres, c.communes)

	for _, brut := range c.communes {
		deAvant, dApres := avant.formes[brut], apres.formes[brut]
		lueAvant := perimetre.projette(deAvant.lecture)
		lueApres := perimetre.projette(dApres.lecture)

		switch {
		// La règle sans tolérance, et elle passe avant les familles : une forme
		// qui n'allumait rien et qui allume maintenant quelque chose est une
		// régression, quelle que soit l'évolution du solde — et même si son
		// aliment est la seule chose qui bouge.
		case perimetre.signaux && len(deAvant.signaux) == 0 && len(dApres.signaux) > 0:
			c.regressions = append(c.regressions, c.ecart(brut, lueAvant, lueApres, deAvant, dApres))
		case memeLecture(lueAvant, lueApres) &&
			(!perimetre.signaux || slices.Equal(deAvant.signaux, dApres.signaux)):
			c.identiques++
		case estUneCanonisation(deAvant, dApres, lueAvant, lueApres, perimetre):
			c.canonisations++
		default:
			// Toute forme qui change sans entrer dans une famille connue est
			// listée en entier, pour être lue.
			c.aRelire = append(c.aRelire, c.ecart(brut, lueAvant, lueApres, deAvant, dApres))
		}
	}
	return c
}

// ecart rend l'écart d'une forme, tel que la sortie le montrera.
func (c comparaison) ecart(brut string, lueAvant, lueApres lectureDUneForme,
	deAvant, dApres formeComparee) ecartDeForme {
	ecart := ecartDeForme{
		brut:  brut,
		avant: lectureComparee{lectureDUneForme: lueAvant},
		apres: lectureComparee{lectureDUneForme: lueApres},
	}
	if c.perimetre.signaux {
		// Jamais nil : un côté qui porte les signaux montre un tableau vide
		// quand rien n'est allumé.
		signauxAvant := append([]string{}, deAvant.signaux...)
		signauxApres := append([]string{}, dApres.signaux...)
		ecart.avant.Signaux = &signauxAvant
		ecart.apres.Signaux = &signauxApres
	}
	return ecart
}

// quantitesDe compte les quantités d'un côté sur les formes données.
func quantitesDe(cote coteCompare, bruts []string) quantitesDUnCote {
	quantites := quantitesDUnCote{
		formes:    len(bruts),
		parSignal: map[string]int{},
		parMotif:  map[string]int{},
	}
	for _, brut := range bruts {
		forme := cote.formes[brut]
		if len(forme.signaux) == 0 {
			quantites.sansSignal++
		}
		if forme.resolu {
			quantites.resolus++
		}
		for _, signal := range forme.signaux {
			quantites.parSignal[signal]++
		}
		if forme.motif != "" {
			quantites.parMotif[forme.motif]++
		}
	}
	return quantites
}

// memeLecture dit si deux lectures portent les mêmes champs. La quantité se
// compare par sa valeur : deux pointeurs distincts sur 100 sont la même
// quantité, et l'absence de quantité n'est pas zéro.
func memeLecture(a, b lectureDUneForme) bool {
	if (a.Quantite == nil) != (b.Quantite == nil) {
		return false
	}
	if a.Quantite != nil && *a.Quantite != *b.Quantite {
		return false
	}
	a.Quantite, b.Quantite = nil, nil
	return a == b
}

// seulLAlimentDiffere dit si l'aliment est le seul champ qui bouge.
func seulLAlimentDiffere(a, b lectureDUneForme) bool {
	if a.Aliment == b.Aliment {
		return false
	}
	a.Aliment, b.Aliment = "", ""
	return memeLecture(a, b)
}

// estUneCanonisation dit si l'aliment, seul champ à bouger, bouge pour la
// raison que la famille nomme.
//
// Que l'aliment soit le seul champ qui change ne suffit pas. Une forme dont
// food passe de « farine » à « » n'a elle aussi changé que de cette colonne,
// et c'est une dégradation franche : rangée en canonisation, elle serait tenue
// hors du verdict sans jamais être listée, ce que l'invariant 4 de la tâche
// interdit. En --base, où le périmètre ne porte pas les signaux, elle ne
// laisserait aucune trace nulle part — et c'est le mode qui doit trancher la
// reprise de l'existant.
//
// Trois gardes, donc, qui disent ce qu'une canonisation est : les signaux ne
// bougent pas quand le périmètre les porte, et l'aliment d'après est non vide
// et résolu — MOTEUR-5 ne réécrit que ce que le lexique retrouve. Tout le
// reste tombe en « à relire » et ressort listé en entier.
//
// Conséquence assumée : une forme qui se résout pour la première fois voit son
// aliment canonisé et son signal non_resolu s'éteindre dans le même mouvement.
// Ses signaux ont donc bougé, et elle est à relire plutôt que comptée ici.
// C'est le sens de la garde — une famille tenue hors du verdict doit se
// tromper du côté de la liste, jamais du côté du silence —, et les quatre
// quantités, qui se comptent ailleurs, n'en sont pas affectées.
func estUneCanonisation(deAvant, dApres formeComparee,
	lueAvant, lueApres lectureDUneForme, perimetre perimetreDesChamps) bool {
	if !seulLAlimentDiffere(lueAvant, lueApres) {
		return false
	}
	if perimetre.signaux && !slices.Equal(deAvant.signaux, dApres.signaux) {
		return false
	}
	return lueApres.Aliment != "" && dApres.resolu
}

// triesDesFormes rend les lignes brutes d'un côté, triées : la sortie est faite
// pour être diffée d'une exécution à l'autre, et ne peut donc pas dépendre du
// parcours d'une table de hachage.
func triesDesFormes(formes map[string]formeComparee) []string {
	bruts := make([]string, 0, len(formes))
	for brut := range formes {
		bruts = append(bruts, brut)
	}
	slices.Sort(bruts)
	return bruts
}

// clesUnies rend, triées, les clés présentes dans l'un ou l'autre des comptes :
// un signal ou un motif qui n'existe que d'un côté est précisément ce qu'on
// cherche.
func clesUnies(a, b map[string]int) []string {
	unies := make(map[string]int, len(a)+len(b))
	for cle := range a {
		unies[cle]++
	}
	for cle := range b {
		unies[cle]++
	}
	return triees(unies)
}

// ecritLaComparaison écrit le compte rendu sur la sortie de la commande.
//
// Une mesure par ligne, tout trié : c'est fait pour être redirigé dans un
// fichier et diffé d'une passe à l'autre.
func ecritLaComparaison(sortie io.Writer, c comparaison) error {
	fmt.Fprintf(sortie, "avant : %s\n", c.avant.origine)
	fmt.Fprintf(sortie, "après : %s\n", c.apres.origine)
	if hors := c.perimetre.horsComparaison(); len(hors) > 0 {
		fmt.Fprintf(sortie, "champs hors comparaison : %s\n", strings.Join(hors, ", "))
	}
	if c.perimetre.pourquoi != "" {
		fmt.Fprintln(sortie, c.perimetre.pourquoi)
	}

	fmt.Fprintf(sortie, "formes communes : %d\n", len(c.communes))
	fmt.Fprintf(sortie, "formes seulement avant : %d\n", len(c.seulementAvant))
	fmt.Fprintf(sortie, "formes seulement après : %d\n", len(c.seulementApres))

	if c.perimetre.signaux {
		ecritLesQuantites(sortie, c.quantitesAvant, c.quantitesApres)
	} else {
		fmt.Fprintln(sortie, "quantités : non mesurables — voir les champs hors comparaison")
	}

	fmt.Fprintf(sortie, "formes identiques : %d\n", c.identiques)
	fmt.Fprintf(sortie, "canonisation de l'aliment : %d (hors verdict)\n", c.canonisations)
	if c.perimetre.signaux {
		fmt.Fprintf(sortie, "régressions : %d\n", len(c.regressions))
	} else {
		fmt.Fprintln(sortie, "régressions : non mesurables — voir les champs hors comparaison")
	}
	fmt.Fprintf(sortie, "à relire : %d\n", len(c.aRelire))

	if err := ecritLesEcarts(sortie, "régression", c.regressions); err != nil {
		return err
	}
	return ecritLesEcarts(sortie, "à relire", c.aRelire)
}

// ecritLesQuantites écrit les quatre quantités de part et d'autre, avec leur
// écart : le compte des formes sans signal, le taux de résolution, le compte de
// chaque signal, et le compte par motif.
func ecritLesQuantites(sortie io.Writer, avant, apres quantitesDUnCote) {
	fmt.Fprintf(sortie, "formes sans signal : avant %d, après %d, écart %s\n",
		avant.sansSignal, apres.sansSignal, ecartEntier(avant.sansSignal, apres.sansSignal))
	fmt.Fprintf(sortie, "résolus : avant %d, après %d, écart %s\n",
		avant.resolus, apres.resolus, ecartEntier(avant.resolus, apres.resolus))
	fmt.Fprintln(sortie, tauxDeResolution(avant, apres))
	for _, signal := range clesUnies(avant.parSignal, apres.parSignal) {
		fmt.Fprintf(sortie, "signal %s : avant %d, après %d, écart %s\n",
			signal, avant.parSignal[signal], apres.parSignal[signal],
			ecartEntier(avant.parSignal[signal], apres.parSignal[signal]))
	}
	for _, motif := range clesUnies(avant.parMotif, apres.parMotif) {
		fmt.Fprintf(sortie, "motif %s : avant %d, après %d, écart %s\n",
			motif, avant.parMotif[motif], apres.parMotif[motif],
			ecartEntier(avant.parMotif[motif], apres.parMotif[motif]))
	}
}

// ecritLesEcarts écrit les formes d'une famille, avec leurs deux lectures.
func ecritLesEcarts(sortie io.Writer, etiquette string, ecarts []ecartDeForme) error {
	for _, ecart := range ecarts {
		fmt.Fprintf(sortie, "%s « %s »\n", etiquette, ecart.brut)
		for _, cote := range []struct {
			nom     string
			lecture lectureComparee
		}{{"avant", ecart.avant}, {"après", ecart.apres}} {
			lue, err := json.Marshal(cote.lecture)
			if err != nil {
				return fmt.Errorf("lecture comparée de %q : %w", ecart.brut, err)
			}
			fmt.Fprintf(sortie, "  %s %s\n", cote.nom, lue)
		}
	}
	return nil
}

// tauxDeResolution rend la ligne du taux : la part des formes dont l'aliment se
// retrouve au lexique, de part et d'autre, et l'écart en points.
//
// C'est le seul chiffre directement comparable à ce que le dépôt moteur mesure
// sur son jeu annoté.
//
// Sans forme commune, il n'y a pas de taux : un quotient sur zéro ne se calcule
// pas, et l'écrire « 0,0 % » serait un chiffre inventé. Les deux côtés portent
// les mêmes formes communes, une seule condition suffit donc.
func tauxDeResolution(avant, apres quantitesDUnCote) string {
	if avant.formes == 0 {
		return "taux de résolution : avant —, après —, écart — points"
	}
	partAvant := part(avant.resolus, avant.formes)
	partApres := part(apres.resolus, apres.formes)
	return fmt.Sprintf("taux de résolution : avant %s, après %s, écart %s points",
		virgule(fmt.Sprintf("%.1f %%", partAvant)),
		virgule(fmt.Sprintf("%.1f %%", partApres)),
		virgule(fmt.Sprintf("%+.1f", partApres-partAvant)))
}

func part(n, total int) float64 {
	return 100 * float64(n) / float64(total)
}

// virgule rend le séparateur décimal du français. La sortie est en français,
// ses nombres aussi.
func virgule(nombre string) string {
	return strings.Replace(nombre, ".", ",", 1)
}

// ecartEntier rend un écart signé. Le signe est ce qui se lit en premier : un
// « +1 » sur un compte de signal est une anomalie à expliquer, pas un détail du
// solde.
func ecartEntier(avant, apres int) string {
	if apres > avant {
		return fmt.Sprintf("+%d", apres-avant)
	}
	return fmt.Sprintf("%d", apres-avant)
}
