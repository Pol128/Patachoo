package recuperation

import (
	"regexp"
	"strconv"
	"strings"
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
type regle struct {
	motif    string
	autorise bool
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
			dernier.regles = append(dernier.regles, regle{motif: valeur, autorise: champ == "allow"})
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
	if err != nil || secondes <= 0 {
		return 0, false
	}
	if annonce := time.Duration(secondes * float64(time.Second)); annonce < delaiAnnonceMax {
		return annonce, true
	}
	return delaiAnnonceMax, true
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
		if !motifCorrespond(regle.motif, chemin) {
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

// motifCorrespond compare un motif de robots.txt à un chemin : * vaut n'importe
// quelle suite, un $ final ancre la fin, et le motif s'aligne toujours sur le
// début du chemin.
func motifCorrespond(motif, chemin string) bool {
	if motif == "" {
		return false
	}
	if !strings.ContainsAny(motif, "*$") {
		return strings.HasPrefix(chemin, motif)
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

	compilee, err := regexp.Compile(expression.String())
	if err != nil {
		// Un motif qu'on ne sait pas lire ne vaut pas interdiction.
		return false
	}
	return compilee.MatchString(chemin)
}
