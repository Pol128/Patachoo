package recuperation

import (
	"bytes"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// L'analyseur de robots.txt. La bibliothèque standard n'en a pas, et les
// analyseurs qui comparent les chemins par un simple préfixe laissent passer
// exactement les règles que les sites de recettes écrivent — un
// « Disallow: /recettes/recette-0* » n'interdit alors rien.
//
// Les règles d'interprétation suivies sont celles de Google : jokers, ancre de
// fin, la règle la plus longue l'emporte (à égalité, Allow gagne), et le groupe
// retenu est celui du jeton d'agent le plus spécifique, à défaut celui de *.
//
// Écrit ici, en Go. Aucune ligne n'est reprise d'un projet sous licence
// incompatible (DOD.md §4).

// regle est un motif de chemin et ce qu'il en dit.
//
// expression porte la forme compilée du motif, quand il a des jokers ou une
// ancre de fin. Elle est fabriquée une fois, à l'analyse : recompilée à chaque
// chemin jugé, elle l'était pour chaque règle du groupe et pour chaque page de
// l'hôte. Nil pour un motif ordinaire, qui se compare par préfixe — et nil
// aussi pour un motif qu'on n'a pas su compiler, qui ne vaut pas interdiction.
type regle struct {
	motif      string
	autorise   bool
	expression *regexp.Regexp
}

// nouvelleRegle fabrique la règle d'un motif, et compile ce qui doit l'être.
// C'est le seul endroit où une regle naît.
func nouvelleRegle(motif string, autorise bool) regle {
	r := regle{motif: motif, autorise: autorise}
	if !strings.ContainsAny(motif, "*$") {
		return r
	}

	var expression strings.Builder
	expression.WriteString("^")
	for i, c := range motif {
		switch {
		case c == '*':
			expression.WriteString(".*")
		case c == '$' && i == len(motif)-1:
			expression.WriteString("$")
		default:
			expression.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	// Une expression qu'on ne sait pas compiler laisse le champ nil, et
	// motifCorrespond en fait un motif qui ne correspond à rien.
	r.expression, _ = regexp.Compile(expression.String())
	return r
}

// groupe rassemble les règles qui valent pour une liste d'agents, et le
// Crawl-delay qu'elles demandent — zéro quand le groupe n'en annonce pas.
type groupe struct {
	agents []string
	regles []regle
	delai  time.Duration
}

// delaiAnnonceMax borne ce qu'un Crawl-delay peut nous imposer.
//
// La valeur vient d'un tiers, et rien ne l'oblige à être raisonnable : un
// robots.txt qui annonce une journée d'attente ne refuse rien explicitement,
// mais tiendrait un import en lot en otage aussi sûrement. Au-delà de la
// borne, nous appliquons la borne — et l'écart se voit dans le rythme, pas
// dans un refus silencieux.
const delaiAnnonceMax = 5 * time.Minute

// tailleMaxRobots borne le robots.txt, indépendamment du plafond de la page.
//
// Il lui faut le sien : une fournée en lit un par hôte, et en retient les
// règles analysées pour toute sa durée. Emprunté à la page, le plafond de
// 5 Mio laisse un lot de 500 hôtes tenir des gibioctets de règles en mémoire,
// et dévore le budget de temps de l'appel au point de faire échouer en
// delai_depasse des pages parfaitement saines. La valeur est celle du
// récolteur de référence, qui ignore lui aussi ce qui dépasse.
const tailleMaxRobots = 512 << 10

// sousLePlafond rend, des octets lus, le texte à analyser.
//
// Le plafond, lui, est tenu par la lecture et par elle seule : cette fonction
// ne le réapplique pas — deux endroits qui bornent la même chose, et l'un des
// deux finit par mentir. Elle lit dans la longueur reçue que la lecture a été
// tranchée, puisque son appelant demande un octet de plus que le plafond.
//
// Le dépassement n'est pas une erreur : le REP demande d'appliquer ce qu'on a
// lu. Mais la lecture s'arrête où elle tombe, éventuellement au milieu d'une
// directive — « Disallow: /recettes » devenu « Disallow: /rec » interdirait
// plus que le site ne l'a écrit. Ce qui suit le dernier saut de ligne lu est
// donc écarté, et seulement quand le plafond a été atteint. L'octet de surplus
// n'est gardé que s'il est lui-même ce saut de ligne, où il ne pèse rien.
func sousLePlafond(lu []byte) string {
	if int64(len(lu)) <= tailleMaxRobots {
		return string(lu)
	}
	fin := bytes.LastIndexByte(lu, '\n')
	if fin < 0 {
		// Pas un seul saut de ligne : tout ce qui a été lu est une directive
		// coupée, il n'en reste rien d'interprétable.
		return ""
	}
	return string(lu[:fin+1])
}

// decisionRobots est ce que le robots.txt d'un hôte dit, une fois lu : de quoi
// trancher n'importe quel chemin de cet hôte sans le redemander.
//
// interditTout se distingue de « aucune règle » : le premier vient d'un serveur
// en panne, que le REP demande de laisser tranquille ; le second d'un
// robots.txt absent, qui n'interdit rien.
type decisionRobots struct {
	interditTout bool
	regles       robots
}

// RobotsRetenus garde ce que le robots.txt de chaque hôte a dit, pour la durée
// que l'appelant lui donne — celle d'une fournée.
//
// Le zéro est utilisable. Sûr à partager entre goroutines : une fournée mène
// plusieurs hôtes de front, et deux d'entre eux peuvent viser le même — c'est
// le cas www.site.fr / site.fr, qui font deux files et un seul robots.txt.
type RobotsRetenus struct {
	mu  sync.Mutex
	par map[string]decisionRobots
}

// lis rend ce qui est retenu pour cette adresse de robots.txt. Un cache nil ne
// retient rien, et c'est ce qui rend l'option facultative sans garde ailleurs.
func (r *RobotsRetenus) lis(adresse string) (decisionRobots, bool) {
	if r == nil {
		return decisionRobots{}, false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	dit, vu := r.par[adresse]
	return dit, vu
}

func (r *RobotsRetenus) garde(adresse string, dit decisionRobots) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.par == nil {
		r.par = map[string]decisionRobots{}
	}
	r.par[adresse] = dit
}

// robots est la décision d'accès d'un hôte.
type robots struct {
	groupes []groupe
}

// analyseRobots lit un robots.txt. Ce qu'il ne comprend pas, il l'ignore : un
// fichier mal écrit n'interdit rien de plus qu'un fichier absent.
func analyseRobots(texte string) robots {
	var r robots
	// Des User-agent consécutifs partagent le même bloc de règles : la
	// première règle rencontrée les referme tous.
	suiteDAgents := false

	for _, brute := range strings.Split(texte, "\n") {
		ligne := strings.TrimSpace(brute)
		if i := strings.Index(ligne, "#"); i >= 0 {
			ligne = strings.TrimSpace(ligne[:i])
		}
		champ, valeur, coupe := strings.Cut(ligne, ":")
		if !coupe {
			continue
		}
		champ = strings.ToLower(strings.TrimSpace(champ))
		valeur = strings.TrimSpace(valeur)

		switch champ {
		case "user-agent":
			if !suiteDAgents {
				r.groupes = append(r.groupes, groupe{})
				suiteDAgents = true
			}
			dernier := &r.groupes[len(r.groupes)-1]
			dernier.agents = append(dernier.agents, strings.ToLower(valeur))
		case "crawl-delay":
			suiteDAgents = false
			if len(r.groupes) == 0 {
				// Une directive avant tout User-agent ne vise personne.
				continue
			}
			if annonce, lisible := dureeAnnoncee(valeur); lisible {
				r.groupes[len(r.groupes)-1].delai = annonce
			}
		case "allow", "disallow":
			suiteDAgents = false
			if len(r.groupes) == 0 {
				continue
			}
			// « Disallow: » sans valeur n'interdit rien : c'est un
			// autorise-tout, pas une interdiction du chemin vide.
			if valeur == "" && champ == "disallow" {
				continue
			}
			dernier := &r.groupes[len(r.groupes)-1]
			dernier.regles = append(dernier.regles, nouvelleRegle(valeur, champ == "allow"))
		default:
			suiteDAgents = false
		}
	}
	return r
}

// dureeAnnoncee lit la valeur d'un Crawl-delay, en secondes, éventuellement
// fractionnaires — les sites en publient. Ce qu'on ne sait pas lire, et ce qui
// ne demande rien, ne ralentit rien.
func dureeAnnoncee(valeur string) (time.Duration, bool) {
	secondes, err := strconv.ParseFloat(valeur, 64)
	// NaN n'est ni positif ni négatif : aucune comparaison ne l'écarte, et il
	// franchirait la borne comme le garde de la ligne au-dessus.
	if err != nil || math.IsNaN(secondes) || secondes <= 0 {
		return 0, false
	}
	// La borne se compare en secondes, avant la conversion : au-delà
	// d'environ 9,2×10⁹ secondes le produit déborde int64 et rend une durée
	// négative, que la borne laisserait passer pour lisible. Le cadencement
	// écarte ensuite les durées négatives, si bien qu'un site annonçant une
	// éternité obtiendrait notre seconde par défaut plutôt que la borne.
	if secondes > delaiAnnonceMax.Seconds() {
		return delaiAnnonceMax, true
	}
	return time.Duration(secondes * float64(time.Second)), true
}

// delaiPour rend le Crawl-delay que le groupe visant agent demande, ou zéro.
//
// Celui d'un autre agent ne nous concerne pas : nous ralentir de trente
// secondes parce que Googlebot est prié de le faire, c'est s'infliger un refus
// qui ne nous vise pas. C'est le même groupe que pour les règles de chemin, et
// c'est groupePour qui le désigne — une seconde façon de le choisir finirait
// par diverger de la première.
func (r robots) delaiPour(agent string) time.Duration {
	if g := r.groupePour(agent); g != nil {
		return g.delai
	}
	return 0
}

// autorise dit si agent peut demander chemin.
func (r robots) autorise(chemin, agent string) bool {
	g := r.groupePour(agent)
	if g == nil {
		return true
	}

	poidsRetenu, autorise := -1, true
	for _, regle := range g.regles {
		if !motifCorrespond(regle, chemin) {
			continue
		}
		poids := len(regle.motif)
		// À longueur égale, Allow l'emporte sur Disallow.
		if poids > poidsRetenu || (poids == poidsRetenu && regle.autorise) {
			poidsRetenu, autorise = poids, regle.autorise
		}
	}
	if poidsRetenu < 0 {
		return true
	}
	return autorise
}

// groupePour retient le groupe du jeton d'agent le plus spécifique qui nous
// désigne, et à défaut celui de *. Les autres ne s'y ajoutent pas.
func (r robots) groupePour(agent string) *groupe {
	nous := strings.ToLower(agent)

	var retenu, etoile *groupe
	plusLong := -1
	for i := range r.groupes {
		for _, jeton := range r.groupes[i].agents {
			if jeton == "*" {
				if etoile == nil {
					etoile = &r.groupes[i]
				}
				continue
			}
			if jeton != "" && strings.Contains(nous, jeton) && len(jeton) > plusLong {
				retenu, plusLong = &r.groupes[i], len(jeton)
			}
		}
	}
	if retenu != nil {
		return retenu
	}
	return etoile
}

// motifCorrespond compare une règle de robots.txt à un chemin : * vaut
// n'importe quelle suite, un $ final ancre la fin, et le motif s'aligne
// toujours sur le début du chemin. Il ne fabrique plus rien : l'expression lui
// arrive compilée depuis l'analyse.
func motifCorrespond(r regle, chemin string) bool {
	switch {
	case r.expression != nil:
		return r.expression.MatchString(chemin)
	case r.motif == "" || strings.ContainsAny(r.motif, "*$"):
		// Le motif vide ne vise rien, et un motif à joker sans expression est
		// un motif qu'on n'a pas su compiler : ni l'un ni l'autre n'interdit.
		return false
	default:
		return strings.HasPrefix(chemin, r.motif)
	}
}
