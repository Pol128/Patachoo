package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Ce que l'annotation ajoute au schéma de l'établi, et ce qu'elle y corrige.
//
// Une migration nouvelle, et non 1789931400_etabli_analyse.go retouché : une
// base déjà installée n'applique pas une migration qu'elle a déjà passée, et
// resterait sur l'ancien schéma.
//
// Deux gestes, et le second est un correctif assumé de PATA-122 :
//
//   - le vocabulaire des verdicts, distinct de tags ;
//   - la cible d'une annotation ramenée à une clé naturelle. analyses_
//     annotations.form était une relation vers analyses_formes, avec cascade.
//     Or ces lignes appartiennent à une passe et sont recréées à chaque
//     analyse : une annotation de forme ne serait pas retrouvée à la passe
//     suivante, contre le critère de survie. La cible devient la ligne brute,
//     comme celle d'un groupe est l'aliment canonique.

// maxVerdictsParAnnotation borne ce qu'une annotation porte de mots.
//
// La même valeur que recipes.tags, et pour la même raison : au-delà, on
// n'étiquette plus, on raconte — et c'est à ça que servent les deux champs de
// texte. Recopiée ici plutôt que lue sur cmd/patachoo : le paquet des
// migrations ne dépend pas du serveur, et c'est ce qui permet de jouer une
// migration depuis une commande.
const maxVerdictsParAnnotation = 20

// champsAjoutesALAnnotation : ce que le down doit retirer, énuméré une fois.
//
// form n'y est pas : le down le rend, il ne le retire pas.
var champsAjoutesALAnnotation = []string{
	"raw", "verdicts", "expected_reading",
	"engine_version", "lexicon_entries", "reading_digest",
}

func init() {
	m.Register(func(app core.App) error {
		verdicts := core.NewBaseCollection("analyses_verdicts")
		verdicts.Fields.Add(
			&core.TextField{Name: "name", Required: true, Presentable: true},
			&core.TextField{Name: "slug", Required: true},
			// Un tag est partageable par construction — il parle du lexique et
			// des règles, pas du corpus — mais il est saisi par un humain qui
			// peut y écrire du corpus. La séparation des deux champs protège
			// le contenu de l'annotation, pas le nom du mot qui la qualifie.
			//
			// Un booléen, donc, et non le cloisonnement par le droit seul :
			// tout compte portant le droit de l'établi peut créer un mot, un
			// mot neuf naît non validé, et la charge partageable ne l'emporte
			// pas tant qu'il ne l'est pas. La parade survit ainsi à un second
			// annotateur.
			//
			// Faux par défaut, comme tout booléen de PocketBase : le défaut
			// d'une autorisation se choisit du côté qui refuse.
			&core.BoolField{Name: "validated"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		// Le slug est la clé d'unicité, pas le nom : « Capture trop » et
		// « capture-trop » doivent se rejoindre, exactement comme sur tags.
		verdicts.AddIndex("idx_analyses_verdicts_slug", true, "slug", "")
		// Les cinq règles restent nulles, donc réservées au superutilisateur
		// par l'API REST — comme les trois autres collections de l'établi. Les
		// pages lisent côté serveur par e.App, qui ne passe pas par les
		// règles, et ce qui les garde est le droit porté par users (PATA-128).
		if err := app.Save(verdicts); err != nil {
			return fmt.Errorf("analyses_verdicts : %w", err)
		}

		annotations, err := app.FindCollectionByNameOrId("analyses_annotations")
		if err != nil {
			return fmt.Errorf("collection analyses_annotations : %w", err)
		}
		// La relation part, et c'est tout le sujet du correctif. Retirée puis
		// remplacée par un champ de texte du même rôle : ce qui disparaît est
		// le lien vers un enregistrement que la passe suivante recrée, pas la
		// cible elle-même.
		annotations.Fields.RemoveByName("form")
		annotations.Fields.Add(
			// La ligne brute, telle que la passe l'a lue. Deux passes du même
			// corpus portent la même, et c'est ce qui fait retrouver
			// l'annotation à la seconde.
			&core.TextField{Name: "raw"},
			// Le verdict, par mots ouverts. Sans cascade : supprimer un mot du
			// vocabulaire ne doit pas emporter les jugements qui l'employaient
			// — le mot est un mot, l'annotation est le jugement.
			&core.RelationField{
				Name:         "verdicts",
				CollectionId: verdicts.Id,
				MaxSelect:    maxVerdictsParAnnotation,
			},
			// La lecture attendue champ à champ, en miroir de
			// analyses_formes.reading : « il fallait lire 2 oignons jaunes ».
			// C'est la matière d'un jeu annoté, donc du corpus — elle reste du
			// côté local, et la charge partageable ne la porte pas.
			&core.JSONField{Name: "expected_reading"},
			// L'empreinte de la passe jugée, recopiée depuis l'enregistrement
			// analyses plutôt que relue à travers la relation : c'est elle qui
			// dit quel parser l'annotation jugeait, et une empreinte lue à
			// l'affichage serait celle de l'analyse courante.
			&core.TextField{Name: "engine_version"},
			&core.NumberField{Name: "lexicon_entries", OnlyInt: true},
			// L'empreinte de la lecture jugée. Le verdict vaut tant que la
			// lecture qu'il jugeait n'a pas changé : à la passe suivante, on
			// recalcule cette empreinte sur la cible et on compare. Identique,
			// le verdict tient ; différente, la cible revient dans l'ordre par
			// défaut avec son verdict précédent affiché.
			&core.TextField{Name: "reading_digest"},
		)
		if err := app.Save(annotations); err != nil {
			return fmt.Errorf("correction de analyses_annotations : %w", err)
		}

		return nil
	}, func(app core.App) error {
		formes, err := app.FindCollectionByNameOrId("analyses_formes")
		if err != nil {
			return fmt.Errorf("collection analyses_formes : %w", err)
		}
		annotations, err := app.FindCollectionByNameOrId("analyses_annotations")
		if err != nil {
			return fmt.Errorf("collection analyses_annotations : %w", err)
		}
		for _, champ := range champsAjoutesALAnnotation {
			annotations.Fields.RemoveByName(champ)
		}
		// La relation d'origine est rendue : un down qui laisse la collection
		// amputée n'est pas un retour en arrière.
		annotations.Fields.Add(&core.RelationField{
			Name:          "form",
			CollectionId:  formes.Id,
			MaxSelect:     1,
			CascadeDelete: true,
		})
		if err := app.Save(annotations); err != nil {
			return fmt.Errorf("retour en arrière de analyses_annotations : %w", err)
		}

		verdicts, err := app.FindCollectionByNameOrId("analyses_verdicts")
		if err != nil {
			return nil // déjà absente : rien à défaire
		}
		if err := app.Delete(verdicts); err != nil {
			return fmt.Errorf("suppression de analyses_verdicts : %w", err)
		}
		return nil
	})
}
