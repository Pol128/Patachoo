package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/Pol128/moteur"
)

// L'ouvrier de l'établi : il passe un corpus entier au parser, hors de toute
// requête. Mesuré le 20/09/2026, trente-trois secondes pour 344 400 lignes sur
// une machine de bureau, et plusieurs minutes sur un Raspberry Pi — c'est un
// travail, pas une requête.
//
// Dans son propre fichier, et non dans ouvrier.go : l'ouvrier d'import fait
// déjà six cent cinquante lignes, et les deux ne partagent que le patron — une
// file, des statuts, une reprise au démarrage. Ce qu'ils font, eux, n'a rien de
// commun : l'un sort sur le réseau une URL à la fois, l'autre lit un corpus de
// bout en bout sans jamais sortir de la machine.
//
// Ce qu'il n'écrit jamais : ingredients et recipes. L'établi lit le carnet, il
// ne le modifie pas — et c'est la seule chose qu'un exploitant doit pouvoir
// tenir pour acquise avant de lancer une passe sur sa base.

// Les trois plafonds d'une analyse, chiffrés ici pour que l'exécution n'ait pas
// à les inventer. Repères du 20/09/2026 : le corpus de la forge fait 320 049
// lignes, la base de l'instance 1 124, et 344 400 lignes prennent 33 s.
//
// Les trois tiennent dans le même fichier et se lisent ensemble : en changer un
// est une relecture d'une ligne, pas une refonte.
const (
	// plafondDeLignesDUneAnalyse borne le nombre de lignes lues. La moitié de
	// marge au-dessus du corpus de la forge, et de quoi refuser un fichier qui
	// n'est manifestement pas un corpus d'ingrédients.
	//
	// Il compte les lignes reçues de la source, vides comprises : un fichier
	// d'un million de lignes blanches est tout autant « pas un corpus », et le
	// compteur de la passe, lui, ne retient que les lignes qui portent quelque
	// chose.
	plafondDeLignesDUneAnalyse = 500_000

	// plafondDOctetsDUneAnalyse borne la taille de l'entrée, sur les octets
	// lus et avant tout découpage en lignes : une seule ligne de 100 Mio
	// n'atteindrait jamais le compteur de lignes.
	plafondDOctetsDUneAnalyse = 32 << 20

	// delaiDUneAnalyse borne la durée d'une passe. Trente-trois secondes pour
	// 344 400 lignes sur une machine de bureau, quelques minutes sur un
	// Raspberry Pi : la marge est large, et trente minutes restent une durée
	// qu'un exploitant accepte de lire dans un statut. Au dépassement, le
	// travail passe en échec ; il ne se poursuit pas.
	delaiDUneAnalyse = 30 * time.Minute
)

// cadenceDeLAvancement espace les écritures du compteur d'avancement. Sans
// elle, une passe sur le corpus de la forge écrirait 320 049 fois dans
// analyses, ce qui coûterait plus cher que l'analyse elle-même.
//
// L'instant se lit sur l'horloge injectée, comme l'ouvrier d'import : sans
// quoi ni cette cadence ni le délai ci-dessus ne se testeraient.
const cadenceDeLAvancement = time.Second

// formesParEcriture borne la taille d'une transaction d'écriture des formes.
//
// Une transaction par forme rendrait la phase d'écriture plus longue que
// l'analyse sur un corpus de cinquante mille formes ; une seule transaction
// pour tout le corpus tiendrait le verrou d'écriture d'un bout à l'autre et ne
// laisserait aucune place à l'avancement. Par paquets, les deux tiennent.
const formesParEcriture = 1_000

// Les deux sources d'une passe, telles que la migration de PATA-122 les
// énumère. Écrites ici une fois : une chaîne recopiée au fil du code finit par
// diverger d'une lettre, et le select de PocketBase le refuserait à l'écriture,
// pas à la lecture.
const (
	sourceInstance = "instance"
	sourceFournie  = "fourni"
)

// moduleDuMoteur est le module dont la version fait l'empreinte d'une passe :
// c'est l'identité du code qui a lu les lignes. moteur.Pack, lui, ne versionne
// que le pack de langue.
const moduleDuMoteur = "github.com/Pol128/moteur"

// versionDuMoteurInconnue est ce que porte une passe faite par un binaire sans
// estampille — une image construite sans .git, un binaire de test. Le même
// aveu que version.go assume déjà pour la sienne : mieux vaut un « inconnue »
// lisible qu'une colonne vide dont personne ne sait si elle n'a pas été
// écrite.
const versionDuMoteurInconnue = "inconnue"

// errAnalyseDejaEnCours est le refus d'un second travail. Un seul à la fois
// pour l'instance, comme l'ouvrier d'import : deux passes menées de front se
// disputeraient la machine et rendraient l'avancement de chacune illisible.
var errAnalyseDejaEnCours = errors.New("une analyse est déjà en cours : l'établi n'en mène qu'une à la fois")

// sourceDeLignes rend les lignes brutes d'un corpus, dans l'ordre.
//
// C'est la couture d'entrée de l'établi, et elle est posée ici plutôt que par
// PATA-124 : la page de lancement arrive après cette tâche, et l'ouvrier ne
// peut donc pas attendre d'elle la forme de son entrée. Il prend une séquence
// de chaînes et ne sait rien de leur provenance — une base, un fichier, un
// téléversement que la page n'aura pas conservé.
//
// La seconde valeur porte l'erreur qui interrompt la lecture. Une source qui
// rend une erreur a fini : rien ne sera plus produit après elle.
type sourceDeLignes iter.Seq2[string, error]

// lignesDeLInstance lit la colonne raw de la collection ingredients.
//
// En flux et non d'un bloc : une instance qui aurait quatre cent mille lignes
// n'a pas à les tenir toutes en mémoire avant que la première soit analysée.
//
// Une lecture, et rien d'autre. C'est la promesse de l'établi, et elle se lit
// dans le SELECT.
func lignesDeLInstance(app core.App) sourceDeLignes {
	return func(yield func(string, error) bool) {
		lignes, err := app.DB().Select("raw").From("ingredients").OrderBy("id").Rows()
		if err != nil {
			yield("", fmt.Errorf("lecture des lignes de l'instance : %w", err))
			return
		}
		defer lignes.Close()

		for lignes.Next() {
			var brut string
			if err := lignes.Scan(&brut); err != nil {
				yield("", fmt.Errorf("lecture d'une ligne de l'instance : %w", err))
				return
			}
			if !yield(brut, nil) {
				return
			}
		}
		if err := lignes.Err(); err != nil {
			yield("", fmt.Errorf("lecture des lignes de l'instance : %w", err))
		}
	}
}

// lignesDUnReader découpe un flux en lignes. C'est la source des tests et celle
// de la sous-commande ; PATA-124 y branchera son téléversement.
//
// Le flux passe par lecteurBorne : le plafond de taille porte sur les octets
// lus, avant tout découpage, et c'est lui qui borne aussi la mémoire que le
// découpage peut demander. Un bufio.Reader plutôt qu'un bufio.Scanner pour
// cette raison même — le second a sa propre borne de jeton, qui parlerait de
// « token too long » là où le plafond de l'établi a un message et une valeur.
func lignesDUnReader(flux io.Reader) sourceDeLignes {
	return func(yield func(string, error) bool) {
		lecteur := bufio.NewReader(&lecteurBorne{flux: flux, reste: plafondDOctetsDUneAnalyse + 1})

		for {
			ligne, err := lecteur.ReadString('\n')
			// La dernière ligne d'un fichier sans retour final arrive avec
			// io.EOF : elle compte comme les autres.
			if ligne != "" && (err == nil || errors.Is(err, io.EOF)) {
				if !yield(ligne, nil) {
					return
				}
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					// Le fragment déjà lu n'est pas rendu : un corpus refusé
					// n'est pas un corpus tronqué.
					yield("", err)
				}
				return
			}
		}
	}
}

// lecteurBorne refuse de rendre plus d'octets que le plafond.
//
// reste part à plafond + 1 : un corpus qui fait exactement la taille maximale
// passe, et c'est le premier octet au-delà qui déclenche le refus.
type lecteurBorne struct {
	flux  io.Reader
	reste int64
}

func (l *lecteurBorne) Read(p []byte) (int, error) {
	if l.reste <= 0 {
		return 0, depassementDOctets()
	}
	if int64(len(p)) > l.reste {
		p = p[:l.reste]
	}
	n, err := l.flux.Read(p)
	l.reste -= int64(n)
	return n, err
}

// depassementDeLignes, depassementDOctets et depassementDuDelai nomment les
// trois refus. Le message donne la valeur du plafond : c'est ce qui permet à
// celui qui le lit de savoir quoi changer, et où.
func depassementDeLignes() error {
	return fmt.Errorf("corpus refusé : plus de %d lignes, le maximum d'une analyse",
		plafondDeLignesDUneAnalyse)
}

func depassementDOctets() error {
	return fmt.Errorf("corpus refusé : plus de %d octets lus, le maximum d'une analyse",
		plafondDOctetsDUneAnalyse)
}

func depassementDuDelai(ecoule time.Duration) error {
	return fmt.Errorf("analyse interrompue : %s écoulées, %s au maximum",
		ecoule.Round(time.Second), delaiDUneAnalyse)
}

// travailDAnalyse est une passe en file : l'enregistrement qui la suit en base,
// et le corpus qui n'existe qu'en mémoire.
type travailDAnalyse struct {
	passe  *core.Record
	lignes sourceDeLignes
}

// ouvrierDAnalyse mène les passes, une à la fois.
type ouvrierDAnalyse struct {
	app       core.App
	horloge   horlogeDuLot
	analyseur *analyseur

	// file porte les travaux qu'un appelant asynchrone — la page de PATA-124 —
	// dépose sans les mener lui-même. Un seul jeton : le refus de la mise en
	// file garantit qu'il n'y a jamais deux travaux à la fois, et le corpus
	// d'un travail fourni ne survit pas au processus de toute façon.
	file chan travailDAnalyse
	// miseEnFile sérialise le « lis puis écris » du refus. Deux requêtes
	// simultanées y liraient l'une après l'autre qu'aucune analyse ne tourne,
	// et en créeraient chacune une.
	miseEnFile sync.Mutex
}

func nouvelOuvrierDAnalyse(app core.App, h horlogeDuLot, a *analyseur) *ouvrierDAnalyse {
	return &ouvrierDAnalyse{
		app:       app,
		horloge:   h,
		analyseur: a,
		file:      make(chan travailDAnalyse, 1),
	}
}

// brancheLOuvrierDAnalyse démarre l'ouvrier avec le serveur et l'arrête avec
// lui, comme brancheLOuvrier.
//
// L'arrêt attend que l'ouvrier ait rendu la main : c'est sa dernière écriture —
// la passe en cours qui passe en échec — qui fait que le démarrage suivant
// retrouve un état cohérent, et elle a besoin d'une base encore ouverte.
func brancheLOuvrierDAnalyse(app core.App, a *analyseur) *ouvrierDAnalyse {
	o := nouvelOuvrierDAnalyse(app, horlogeSysteme{}, a)
	ctx, arrete := context.WithCancel(context.Background())
	fini := make(chan struct{})
	// Le même garde-fou que l'ouvrier d'import : OnServe vient de la commande
	// serve, OnTerminate du traitement du signal, et rien ne les ordonne.
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
		// Une sous-commande — une analyse lancée à la main, une migration —
		// termine sans jamais avoir servi : il n'y a alors personne à attendre.
		if demarre.Load() {
			<-fini
		}
		return e.Next()
	})

	return o
}

// tourne reprend ce qu'un arrêt a laissé, puis mène les travaux qu'on lui
// dépose, tant que le contexte vit.
//
// Pas de sondage de la file : contrairement aux lignes d'un lot d'import, le
// corpus d'une passe ne vit qu'en mémoire — une passe qu'aucun processus vivant
// n'a mise en file n'a plus de corpus à lire, et la reprise en échec est
// précisément ce qui en tire les conséquences.
func (o *ouvrierDAnalyse) tourne(ctx context.Context) {
	if err := o.rendLesAnalysesInterrompues(); err != nil {
		o.app.Logger().Error("reprise des analyses en cours", "erreur", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case travail := <-o.file:
			if err := o.mene(ctx, travail.passe, travail.lignes); err != nil {
				o.app.Logger().Error("analyse de corpus",
					"analyse", travail.passe.Id, "erreur", err)
			}
		}
	}
}

// rendLesAnalysesInterrompues passe en échec les analyses qu'un arrêt a
// laissées en cours.
//
// En échec, et non à faire — c'est l'inverse de l'ouvrier d'import, et c'est
// voulu : le corpus d'un travail fourni n'existe plus au redémarrage, et une
// reprise le rejouerait à vide indéfiniment. Une passe sur la base de
// l'instance serait rejouable, mais la distinguer ici ferait deux
// comportements là où la seule chose à dire à l'exploitant est « celle-ci n'est
// pas allée au bout ».
func (o *ouvrierDAnalyse) rendLesAnalysesInterrompues() error {
	passes, err := o.app.FindAllRecords("analyses", dbx.HashExp{"status": statutEnCours})
	if err != nil {
		return fmt.Errorf("lecture des analyses interrompues : %w", err)
	}

	for _, passe := range passes {
		if err := o.clot(passe, statutEchec); err != nil {
			return err
		}
	}
	return nil
}

// metEnFile réserve la passe et la dépose : c'est le chemin d'un appelant qui
// ne veut pas attendre, et celui que la page de lancement prendra.
func (o *ouvrierDAnalyse) metEnFile(source string, lignes sourceDeLignes) (*core.Record, error) {
	passe, err := o.reserve(source)
	if err != nil {
		return nil, err
	}

	// Jamais bloquant : la réservation vient de garantir qu'aucune autre passe
	// n'occupe la file, et le jeton n'est rendu qu'à la fin de la précédente.
	o.file <- travailDAnalyse{passe: passe, lignes: lignes}
	return passe, nil
}

// lance réserve la passe et la mène sur la goroutine de l'appelant : c'est le
// chemin de la sous-commande, derrière laquelle aucun ouvrier ne tourne.
//
// La passe est rendue même quand l'analyse échoue : c'est par elle que
// l'appelant retrouve le travail dont il veut lire le statut.
func (o *ouvrierDAnalyse) lance(ctx context.Context, source string, lignes sourceDeLignes) (*core.Record, error) {
	passe, err := o.reserve(source)
	if err != nil {
		return nil, err
	}
	return passe, o.mene(ctx, passe, lignes)
}

// reserve écrit la passe en cours, ou refuse parce qu'une autre l'est déjà.
//
// Le refus se prononce ici, à la mise en file, et sur la base plutôt que sur un
// état en mémoire : une sous-commande et un serveur sont deux processus, et
// c'est la base qu'ils ont en commun.
func (o *ouvrierDAnalyse) reserve(source string) (*core.Record, error) {
	o.miseEnFile.Lock()
	defer o.miseEnFile.Unlock()

	enCours, err := o.app.CountRecords("analyses", dbx.HashExp{"status": statutEnCours})
	if err != nil {
		return nil, fmt.Errorf("décompte des analyses en cours : %w", err)
	}
	if enCours > 0 {
		return nil, errAnalyseDejaEnCours
	}

	collection, err := o.app.FindCollectionByNameOrId("analyses")
	if err != nil {
		return nil, fmt.Errorf("collection analyses : %w", err)
	}

	passe := core.NewRecord(collection)
	passe.Set("source", source)
	passe.Set("status", statutEnCours)
	passe.Set("lines", 0)
	passe.Set("forms", 0)
	// L'empreinte dès la création, et non à la clôture : une passe qui échoue
	// dit tout de même quel parser elle faisait tourner, et c'est souvent
	// celle-là qu'on relit.
	passe.Set("engine_version", versionDuMoteur(estampilleDuBinaire()))
	passe.Set("lexicon_entries", o.analyseur.lexique.Formes())
	if err := o.app.Save(passe); err != nil {
		return nil, fmt.Errorf("création de l'analyse : %w", err)
	}
	return passe, nil
}

// mene mène une passe jusqu'à son terme et lui donne son statut, quel qu'il
// soit. Une passe laissée en cours n'est plus réclamée par personne : c'est le
// redémarrage suivant qui la fermerait, et rien ne dirait pourquoi.
func (o *ouvrierDAnalyse) mene(ctx context.Context, passe *core.Record, lignes sourceDeLignes) error {
	if err := o.analyse(ctx, passe, lignes); err != nil {
		// La cause part au journal : le schéma de l'établi ne porte pas de
		// colonne pour elle, et la sous-commande, elle, rend l'erreur à son
		// appelant.
		o.app.Logger().Error("analyse de corpus", "analyse", passe.Id, "erreur", err)
		if echec := o.clot(passe, statutEchec); echec != nil {
			return errors.Join(err, echec)
		}
		return err
	}
	return o.clot(passe, statutTermine)
}

// clot pose le statut final et la date de fin.
//
// finished plutôt que updated : chaque écriture d'avancement fait bouger le
// second, et la fin d'une passe ne s'en déduit donc pas.
func (o *ouvrierDAnalyse) clot(passe *core.Record, statut string) error {
	fin, err := types.ParseDateTime(o.horloge.Maintenant())
	if err != nil {
		return fmt.Errorf("date de fin de l'analyse %s : %w", passe.Id, err)
	}

	passe.Set("status", statut)
	passe.Set("finished", fin)
	if err := o.app.Save(passe); err != nil {
		return fmt.Errorf("clôture de l'analyse %s : %w", passe.Id, err)
	}
	return nil
}

// analyse mène les deux temps d'une passe : lire le corpus, puis écrire ses
// formes.
//
// Deux temps et non un seul, parce que la déduplication a besoin d'avoir tout
// vu : une forme ne connaît son nombre d'occurrences qu'à la dernière ligne.
func (o *ouvrierDAnalyse) analyse(ctx context.Context, passe *core.Record, lignes sourceDeLignes) error {
	avance := &avancement{ouvrier: o, passe: passe, dernier: o.horloge.Maintenant()}
	debut := avance.dernier

	formes, lues, err := o.litLeCorpus(ctx, lignes, debut, avance)
	if err != nil {
		return err
	}

	return o.ecritLesFormes(ctx, passe, formes, lues, debut, avance)
}

// formeAnalysee est une ligne distincte du corpus et le nombre de fois qu'elle
// y apparaît.
type formeAnalysee struct {
	brut        string
	occurrences int
}

// litLeCorpus déduplique les lignes en formes, dans l'ordre de leur première
// apparition, et rend le nombre de lignes lues.
//
// La déduplication précède la lecture par le moteur, et non l'inverse : deux
// lignes brutes identiques ont la même lecture par construction, et le corpus
// de la forge ne compte qu'une forme distincte pour une dizaine de lignes.
// Lire d'abord reviendrait à payer dix fois le même travail.
//
// La forme est la ligne brute, espaces de bord retirés. Deux lignes qui ne
// diffèrent que par la casse ou les accents restent deux formes : c'est le brut
// que l'écran montre, et l'index FTS5 de PATA-122 porte déjà le repli.
func (o *ouvrierDAnalyse) litLeCorpus(ctx context.Context, lignes sourceDeLignes,
	debut time.Time, avance *avancement) ([]*formeAnalysee, int, error) {
	var (
		formes  []*formeAnalysee
		parBrut = map[string]*formeAnalysee{}
		lues    int
		recues  int
		octets  int64
		echec   error
	)

	for ligne, err := range lignes {
		if err != nil {
			return nil, 0, err
		}

		recues++
		if recues > plafondDeLignesDUneAnalyse {
			echec = depassementDeLignes()
			break
		}
		octets += int64(len(ligne))
		if octets > plafondDOctetsDUneAnalyse {
			echec = depassementDOctets()
			break
		}

		maintenant := o.horloge.Maintenant()
		if err := o.tientEncore(ctx, debut, maintenant); err != nil {
			echec = err
			break
		}

		// Ni lue ni comptée : une ligne d'espaces n'est pas un ingrédient, et
		// une base exportée en porte.
		brut := strings.TrimSpace(ligne)
		if brut == "" {
			continue
		}
		lues++

		if forme, vue := parBrut[brut]; vue {
			forme.occurrences++
		} else {
			forme = &formeAnalysee{brut: brut, occurrences: 1}
			parBrut[brut] = forme
			formes = append(formes, forme)
		}

		if err := avance.pousse(maintenant, lues, 0, false); err != nil {
			return nil, 0, err
		}
	}
	if echec != nil {
		return nil, 0, echec
	}

	return formes, lues, nil
}

// ecritLesFormes lit chaque forme par le moteur, en calcule les signaux, et
// l'écrit. Par paquets : voir formesParEcriture.
func (o *ouvrierDAnalyse) ecritLesFormes(ctx context.Context, passe *core.Record,
	formes []*formeAnalysee, lues int, debut time.Time, avance *avancement) error {
	collection, err := o.app.FindCollectionByNameOrId("analyses_formes")
	if err != nil {
		return fmt.Errorf("collection analyses_formes : %w", err)
	}

	for debutDuPaquet := 0; debutDuPaquet < len(formes); debutDuPaquet += formesParEcriture {
		paquet := formes[debutDuPaquet:min(debutDuPaquet+formesParEcriture, len(formes))]

		maintenant := o.horloge.Maintenant()
		if err := o.tientEncore(ctx, debut, maintenant); err != nil {
			return err
		}

		err := o.app.RunInTransaction(func(txApp core.App) error {
			for _, forme := range paquet {
				if err := txApp.Save(o.formeEcrite(collection, passe, forme)); err != nil {
					return fmt.Errorf("écriture de la forme %q : %w", forme.brut, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}

		if err := avance.pousse(maintenant, lues, debutDuPaquet+len(paquet), false); err != nil {
			return err
		}
	}

	return avance.pousse(o.horloge.Maintenant(), lues, len(formes), true)
}

// formeEcrite rend l'enregistrement d'une forme : la lecture du moteur, ce
// qu'elle donne des colonnes de PATA-122, et ses signaux.
//
// La provenance — recipe et line — n'est pas posée : elle se traite avec la
// page détail (PATA-126), et rien n'est écrit pour elle ici.
func (o *ouvrierDAnalyse) formeEcrite(collection *core.Collection, passe *core.Record,
	forme *formeAnalysee) *core.Record {
	lu := o.analyseur.lit(forme.brut)
	entree, resolu := o.analyseur.resout(lu)

	enregistrement := core.NewRecord(collection)
	enregistrement.Set("analysis", passe.Id)
	enregistrement.Set("raw", forme.brut)
	enregistrement.Set("occurrences", forme.occurrences)
	enregistrement.Set("pattern", lu.Motif)
	enregistrement.Set("reading", lectureEnJSON(lu))
	enregistrement.Set("food", lu.Aliment)
	enregistrement.Set("resolved", resolu)
	// La catégorie est celle de l'entrée du lexique : une forme que rien ne
	// résout n'en a pas, et c'est ce que l'écran agrégé montrera.
	enregistrement.Set("category", entree.Label)
	enregistrement.Set("signals", o.analyseur.signaux(forme.brut, lu))

	return enregistrement
}

// lectureDUneForme est la lecture champ à champ, telle que la colonne reading
// la porte.
//
// Les clés sont celles des colonnes de ingredients — quantity, unit, food,
// note, optional — là où elles portent la même chose : l'écran de détail montre
// une lecture à côté d'une fiche, et deux vocabulaires pour les mêmes cinq
// champs se paieraient à chaque relecture. partitive s'y ajoute, que le schéma
// des ingrédients ne garde pas.
type lectureDUneForme struct {
	Quantite  *float64 `json:"quantity"`
	Unite     string   `json:"unit"`
	Partitif  string   `json:"partitive"`
	Aliment   string   `json:"food"`
	Note      string   `json:"note"`
	Optionnel bool     `json:"optional"`
}

// lectureEnJSON rend ce qu'une lecture met dans la colonne reading.
//
// unit prend le texte de la ligne et non l'abréviation canonique : c'est une
// relecture de parser, et ce qui s'y juge est l'écart entre ce que la ligne
// écrit et ce que le moteur en fait. champsLus, qui alimente une fiche, prend
// l'inverse pour la raison inverse.
func lectureEnJSON(lu *moteur.Ingredient) lectureDUneForme {
	return lectureDUneForme{
		Quantite:  lu.Quantite,
		Unite:     lu.UniteTexte,
		Partitif:  lu.Partitif,
		Aliment:   lu.Aliment,
		Note:      lu.Note,
		Optionnel: lu.Optionnel,
	}
}

// tientEncore dit si la passe peut continuer : le serveur ne s'arrête pas, et
// le délai n'est pas dépassé.
func (o *ouvrierDAnalyse) tientEncore(ctx context.Context, debut, maintenant time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ecoule := maintenant.Sub(debut); ecoule > delaiDUneAnalyse {
		return depassementDuDelai(ecoule)
	}
	return nil
}

// avancement pousse les compteurs de la passe, au plus une fois par seconde.
//
// Il n'est pas protégé par un verrou : une passe est menée d'un bout à l'autre
// par une seule goroutine, et c'est le refus du second travail qui le garantit.
type avancement struct {
	ouvrier *ouvrierDAnalyse
	passe   *core.Record
	dernier time.Time
}

// pousse écrit les compteurs si la cadence le permet, ou si on le lui impose —
// ce qui est le cas une fois, à la fin.
func (a *avancement) pousse(maintenant time.Time, lues, formes int, impose bool) error {
	if !impose && maintenant.Sub(a.dernier) < cadenceDeLAvancement {
		return nil
	}
	a.dernier = maintenant

	a.passe.Set("lines", lues)
	a.passe.Set("forms", formes)
	if err := a.ouvrier.app.Save(a.passe); err != nil {
		return fmt.Errorf("avancement de l'analyse %s : %w", a.passe.Id, err)
	}
	return nil
}

// versionDuMoteur rend la version du module qui a lu les lignes.
//
// Elle reçoit l'estampille plutôt que de la lire, comme versionComposee : go
// test n'inscrit aucune dépendance dans ReadBuildInfo, et une fonction qui
// l'appellerait elle-même ne serait vérifiable sur aucun de ses cas.
func versionDuMoteur(infos *debug.BuildInfo) string {
	if infos == nil {
		return versionDuMoteurInconnue
	}
	for _, module := range infos.Deps {
		if module.Path == moduleDuMoteur && module.Version != "" {
			return module.Version
		}
	}
	return versionDuMoteurInconnue
}

// estampilleDuBinaire rend ce que la chaîne de compilation a inscrit, ou rien
// du tout — ReadBuildInfo échoue sur un binaire qui n'a pas été construit en
// mode module.
func estampilleDuBinaire() *debug.BuildInfo {
	infos, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return infos
}
