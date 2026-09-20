package main

import (
	"context"
	"sync"
	"time"

	"github.com/Pol128/Patachoo/recuperation"
)

// Le rythme des requêtes sortantes de l'import en lot, hôte par hôte.
//
// Le seau à jetons du récolteur de référence a déjà tranché la question, et
// c'est son principe qui est porté ici : un jeton par hôte, pas de rafale
// accumulée, l'attente calculée depuis la dernière requête. Ce qu'il fait en
// plus — ajustement AIMD sur les 429, remontée progressive, réglage à chaud —
// n'a pas de sens pour une fournée que quelqu'un a collée à la main : ce
// portage-ci s'arrête là où la tâche s'arrête. Aucune ligne n'en est reprise ;
// c'est du Python, et DOD.md §4 tient.

// delaiEntreRequetes est notre propre politesse : au plus une requête par
// seconde vers un même hôte. Un Crawl-delay plus généreux ne l'accélère pas —
// un site qui nous autorise dix requêtes par seconde ne nous oblige pas à les
// faire.
const delaiEntreRequetes = time.Second

// attenteMaxUnitaire est ce qu'un chemin interactif accepte d'attendre son tour
// avant d'y renoncer, quelle que soit l'origine de l'attente.
//
// Le lot, lui, attend sans limite : il est asynchrone, personne n'est devant
// lui, et attendre est la politesse. L'unitaire part d'une session qui attend
// sa réponse, et cinq secondes d'écran qui tourne sont déjà beaucoup — un hôte
// qu'une fournée parcourt à un Crawl-delay de cinq minutes en ferait des
// minutes. Laisser du travail de fond bloquer sans plafond du travail
// interactif est la mauvaise priorité.
//
// Et une attente synchrone non bornée est en soi un levier bon marché : cent
// adresses d'un site qu'on fait parcourir par ailleurs immobiliseraient cent
// gestionnaires pendant des minutes.
const attenteMaxUnitaire = 5 * time.Second

// horlogeDuLot est le temps que l'ouvrier lit et attend.
//
// Injectable pour la même raison que le transport de recuperation : une
// constante en dur ne se teste pas, et personne n'écrit un test qui patiente
// dix secondes.
//
// Nommée ainsi et non « horloge » : saisons.go porte déjà, sous ce nom-là, la
// couture par où la date du jour entre. Deux temps différents — l'un dit le
// mois qu'il est, l'autre fait patienter entre deux requêtes — et un seul
// paquet pour les deux.
//
// Files signale l'ouverture (+1) et la fermeture (-1) d'une file d'hôte.
// L'horloge du système n'en fait rien — le temps passe sans qu'on le lui
// demande. C'est l'horloge virtuelle des tests qui s'en sert : elle n'avance
// que lorsque toutes les files ouvertes attendent, c'est-à-dire quand plus
// rien ne peut progresser sans que le temps passe.
type horlogeDuLot interface {
	Maintenant() time.Time
	// Attends rend la main au bout de d, ou dès que le contexte est annulé —
	// et rend alors son erreur.
	Attends(ctx context.Context, d time.Duration) error
	Files(delta int)
}

// horlogeSysteme est le temps qui passe tout seul.
type horlogeSysteme struct{}

func (horlogeSysteme) Maintenant() time.Time { return time.Now() }

func (horlogeSysteme) Attends(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	minuteur := time.NewTimer(d)
	defer minuteur.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-minuteur.C:
		return nil
	}
}

func (horlogeSysteme) Files(int) {}

// cadence tient le rythme, hôte par hôte. Une seule pour l'instance : deux
// lots menés de front sur le même domaine y feraient deux requêtes par
// seconde, et le cadencement ne voudrait plus rien dire.
type cadence struct {
	horloge horlogeDuLot

	mu sync.Mutex
	// dernier est l'instant réservé par la requête précédente vers cet hôte,
	// et non le prochain créneau : c'est ce qui permet à un Crawl-delay appris
	// entre-temps de s'appliquer dès la requête suivante.
	//
	// Rien n'en sort : une entrée par hôte pèse une clé et un instant, et le
	// nombre d'hôtes distincts qu'une instance verra se compte en milliers.
	// Une péremption coûterait plus cher à écrire et à tester qu'elle ne
	// rendrait.
	dernier map[string]time.Time
	// delais garde ce que chaque hôte a annoncé.
	delais map[string]time.Duration
}

// cadenceDeLInstance est le tour de rôle unique du service : l'ouvrier du lot
// et les chemins unitaires — l'import d'une URL, l'image que le formulaire fait
// télécharger — y passent tous.
//
// Une variable de paquet, et non un champ de l'ouvrier, parce que les
// gestionnaires de routes sont des fonctions de paquet sans dépendance
// injectée (brancheLesRoutes) : c'est le seul porteur qu'ils atteignent, et
// c'est aussi le seul point où un test peut en substituer une autre — la
// sienne, bâtie sur l'horloge virtuelle.
//
// Deux cadences, une par chemin, reviendraient à deux requêtes par seconde vers
// un même hôte, et le cadencement ne voudrait plus rien dire : c'est le même
// argument qui veut qu'il n'y ait qu'un ouvrier.
var cadenceDeLInstance = nouvelleCadence(horlogeSysteme{})

func nouvelleCadence(h horlogeDuLot) *cadence {
	return &cadence{
		horloge: h,
		dernier: map[string]time.Time{},
		delais:  map[string]time.Duration{},
	}
}

// attendSonTour réserve le prochain créneau de l'hôte et patiente jusque-là.
//
// La réservation et l'attente sont séparées, et c'est ce qui fait progresser
// les hôtes de front : le verrou n'est tenu que le temps du calcul, jamais
// pendant l'attente. Un créneau réservé puis abandonné — contexte annulé — est
// perdu, et c'est sans conséquence : il ne fait qu'espacer un peu plus.
//
// attenteMax borne l'attente entière, et zéro ne borne rien. Au-delà, le tour
// n'est pas pris : renoncer après l'avoir réservé pousserait la file de l'hôte
// pour une requête qui ne partira pas, et suffirait à affamer un lot en cours
// en renonçant en boucle.
//
// Ce qui a été attendu est rendu à l'appelant : c'est ainsi qu'un appel de
// recuperation, qui émet deux requêtes, tient une borne valant pour lui entier
// et non pour chacune. L'attente se lit ici et nulle part ailleurs — sur
// l'horloge injectée, qui est virtuelle en test.
//
// L'attente entière, et non la seule part qui vient d'un autre appel : ce que
// l'hôte réclame pour lui-même compte dedans. Autrement, c'est le site visé qui
// décide combien de temps un gestionnaire HTTP reste immobilisé — le
// Crawl-delay est rapporté avant la requête de page du même appel,
// delaiAnnonceMax en accepte cinq minutes, et cette attente est prise hors du
// budget delaiMax de echange : plus rien ne la borne. Un serveur hostile
// annoncerait « Crawl-delay: 300 » et immobiliserait un gestionnaire par hôte
// importé, le plafond de dix imports par minute n'y changeant rien puisqu'un
// sous-domaine par import suffit à en changer.
//
// Ce que ce refus coûte, et qui est assumé : un site annonçant plus que la
// borne n'est pas importable à la main. L'import en lot, lui, l'attend sans
// limite — personne n'est devant son écran —, et c'est ce que le message du
// renoncement dit à l'utilisateur.
func (c *cadence) attendSonTour(ctx context.Context, hote string, attenteMax time.Duration) (time.Duration, error) {
	c.mu.Lock()
	maintenant := c.horloge.Maintenant()
	creneau := maintenant
	if precedent, vu := c.dernier[hote]; vu {
		// Pas de rafale accumulée : un hôte laissé de côté dix minutes ne
		// gagne pas dix minutes de jetons, il repart de maintenant.
		if prochain := precedent.Add(c.delaiDe(hote)); prochain.After(creneau) {
			creneau = prochain
		}
	}
	attente := creneau.Sub(maintenant)
	if attenteMax > 0 && attente > attenteMax {
		c.mu.Unlock()
		return 0, recuperation.ErrAttenteTropLongue
	}
	c.dernier[hote] = creneau
	c.mu.Unlock()

	if err := c.horloge.Attends(ctx, attente); err != nil {
		// L'attente a été coupée en chemin : ce qu'elle a duré n'a plus
		// d'appelant à qui le rendre, l'appel s'arrête ici.
		return 0, err
	}
	return attente, nil
}

// retiens garde le Crawl-delay qu'un hôte annonce, quand il dépasse le nôtre.
// Cadencer à une requête par seconde un site qui en demande une toutes les dix
// secondes, c'est ignorer un refus poli.
func (c *cadence) retiens(hote string, annonce time.Duration) {
	if annonce <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if annonce > c.delais[hote] {
		c.delais[hote] = annonce
	}
}

// cadenceDeRecuperation présente la cadence sous le nom que recuperation attend
// d'elle. Les majuscules viennent de son interface ; le reste du fichier garde
// le sien.
type cadenceDeRecuperation struct{ *cadence }

func (c cadenceDeRecuperation) AttendSonTour(ctx context.Context, hote string, attenteMax time.Duration) (time.Duration, error) {
	return c.attendSonTour(ctx, hote, attenteMax)
}

func (c cadenceDeRecuperation) Retiens(hote string, annonce time.Duration) {
	c.retiens(hote, annonce)
}

// delaiDe rend l'écart à respecter vers un hôte. À appeler sous le verrou.
func (c *cadence) delaiDe(hote string) time.Duration {
	if annonce := c.delais[hote]; annonce > delaiEntreRequetes {
		return annonce
	}
	return delaiEntreRequetes
}

// enFiles mène les tâches de front et rend la main quand toutes ont fini.
//
// Les files s'annoncent toutes à l'horloge avant qu'aucune ne démarre. Ce
// n'est pas un détail d'ordonnancement : l'horloge virtuelle des tests
// n'avance que lorsque toutes les files annoncées attendent, et une file
// annoncée en retard la ferait sauter avant que celle-ci ait émis sa première
// requête — le cadencement se lirait alors comme une mise en file d'attente.
func enFiles(h horlogeDuLot, taches []func()) {
	for range taches {
		h.Files(1)
	}

	var ensemble sync.WaitGroup
	for _, tache := range taches {
		ensemble.Add(1)
		go func() {
			defer ensemble.Done()
			defer h.Files(-1)

			tache()
		}()
	}
	ensemble.Wait()
}
