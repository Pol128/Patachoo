package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Ce que l'établi d'analyse de corpus écrit en base : une passe, ses formes,
// et les verdicts portés dessus.
//
// Une migration nouvelle, et non le schéma initial retouché : une base déjà
// installée n'applique pas une migration qu'elle a déjà passée, et se
// retrouverait sans ces collections.

// sourcesDUnePasse : d'où viennent les lignes analysées. La base de
// l'instance, ou un corpus fourni — un fichier déposé, qui ne crée aucune
// recette.
var sourcesDUnePasse = []string{"instance", "fourni"}

// statutsDUnePasse : trois là où un lot d'import n'en a que deux. Un lot dont
// chaque ligne a un sort définitif est terminé quel que soit ce sort ; une
// passe d'analyse, elle, peut échouer en bloc — le corpus est illisible, le
// moteur refuse de démarrer.
var statutsDUnePasse = []string{"en_cours", "termine", "echec"}

// tableDeLIndexDesFormes : l'index de recherche de l'établi, sur le brut et
// sur l'aliment canonique.
//
// Les deux colonnes, et pas seulement le brut : l'aliment canonique n'apparaît
// pas forcément dans la ligne qui l'a produit — « crème fraîche » pour « 20 cl
// de crème fraîche épaisse » —, et c'est par lui que la vue agrégée cherchera.
//
// form_id porte l'identifiant texte de la forme : les identifiants PocketBase
// sont des chaînes de 15 caractères, ils ne peuvent pas servir de rowid.
// UNINDEXED pour qu'ils ne soient pas cherchables.
//
// La tokenisation est celle de recipes_fts, et pour la même raison : « Crème »
// LIKE '%creme%' est faux, et en français on tape volontiers sans accents.
const tableDeLIndexDesFormes = `CREATE VIRTUAL TABLE analyses_formes_fts USING fts5(
	form_id UNINDEXED,
	raw,
	food,
	tokenize = "unicode61 remove_diacritics 2"
)`

// declencheursDeLIndexDesFormes : la tenue à jour de l'index, par déclencheurs
// SQL plutôt que par des hooks PocketBase — l'argument complet est au-dessus
// de declencheursDeLIndex, dans 1788858000_recherche_fts5.go. Résumé : le
// déclencheur écrit dans la transaction de l'écriture, l'index ne peut donc pas
// diverger, et il vaut pour tout chemin d'écriture, y compris le prochain code
// qui oubliera d'appeler le hook.
//
// Trois, et pas un de plus : une forme n'est dénormalisée d'aucune autre
// table, ses deux colonnes indexées lui appartiennent.
var declencheursDeLIndexDesFormes = []declencheur{
	{nom: "analyses_formes_fts_apres_insertion", sql: `CREATE TRIGGER analyses_formes_fts_apres_insertion AFTER INSERT ON analyses_formes BEGIN
		INSERT INTO analyses_formes_fts(form_id, raw, food)
		VALUES (new.id, new.raw, new.food);
	END`},

	// La provenance d'une forme peut être vidée bien après l'analyse — une
	// recette effacée —, et ce chemin-là passe aussi par une mise à jour.
	{nom: "analyses_formes_fts_apres_maj", sql: `CREATE TRIGGER analyses_formes_fts_apres_maj AFTER UPDATE ON analyses_formes BEGIN
		UPDATE analyses_formes_fts
		SET raw = new.raw, food = new.food
		WHERE form_id = new.id;
	END`},

	{nom: "analyses_formes_fts_apres_suppression", sql: `CREATE TRIGGER analyses_formes_fts_apres_suppression AFTER DELETE ON analyses_formes BEGIN
		DELETE FROM analyses_formes_fts WHERE form_id = old.id;
	END`},
}

// collectionsDeLEtabliDuPlusJeuneAuPlusVieux : l'ordre de suppression du down.
// Les annotations référencent les formes, qui référencent les passes.
var collectionsDeLEtabliDuPlusJeuneAuPlusVieux = []string{
	"analyses_annotations",
	"analyses_formes",
	"analyses",
}

func init() {
	m.Register(func(app core.App) error {
		recettes, err := app.FindCollectionByNameOrId("recipes")
		if err != nil {
			return fmt.Errorf("collection recipes : %w", err)
		}

		passes := core.NewBaseCollection("analyses")
		passes.Fields.Add(
			&core.SelectField{Name: "source", Values: sourcesDUnePasse, MaxSelect: 1},
			&core.SelectField{Name: "status", Values: statutsDUnePasse, MaxSelect: 1},
			// Les lignes lues et les formes distinctes qu'elles donnent. Le
			// rapport de la passe est le rapport entre les deux.
			&core.NumberField{Name: "lines", OnlyInt: true},
			&core.NumberField{Name: "forms", OnlyInt: true},
			// L'empreinte de la passe. Sans elle, une annotation ne dit plus
			// quel parser elle jugeait, et devient illisible à la montée de
			// version suivante.
			//
			// La version du module, celle que go.mod épingle et que
			// runtime/debug.ReadBuildInfo rend : c'est l'identité du code.
			// moteur.Pack.Version, lui, ne versionne que le pack de langue.
			&core.TextField{Name: "engine_version"},
			// La taille du référentiel qui a servi, et ce qu'elle compte
			// vraiment : les écritures reconnues — nom, pluriel et alias —,
			// et non les entrées du lexique, qui sont moins nombreuses. Le
			// nom de la colonne dit « entrées » et il reste : la renommer
			// demanderait une migration sur une base déjà installée, pour un
			// champ dont le seul usage est de distinguer deux passes. C'est
			// ce commentaire qui porte la vérité du chiffre (PATA-120).
			&core.NumberField{Name: "lexicon_entries", OnlyInt: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
			// La fin ne se déduit pas d'updated, que chaque écriture
			// d'avancement fait bouger.
			&core.DateField{Name: "finished"},
		)
		// Pas de created_by : la collection est réservée aux
		// superutilisateurs, qui ne vivent pas dans users mais dans
		// _superusers. Une relation vers users y serait fausse.
		if err := app.Save(passes); err != nil {
			return fmt.Errorf("analyses : %w", err)
		}

		formes := core.NewBaseCollection("analyses_formes")
		formes.Fields.Add(
			// Même construction que import_urls.batch : supprimer une passe
			// emporte ses formes, sinon la base garde le détail d'une mesure
			// que plus rien ne date.
			&core.RelationField{
				Name:          "analysis",
				CollectionId:  passes.Id,
				MaxSelect:     1,
				Required:      true,
				CascadeDelete: true,
			},
			// Une ligne par forme distincte, pas par occurrence : deux lignes
			// brutes identiques ont par définition la même lecture. Même nom
			// que ingredients.raw, qui porte la même chose.
			&core.TextField{Name: "raw", Required: true, Presentable: true},
			&core.NumberField{Name: "occurrences", OnlyInt: true},
			&core.TextField{Name: "pattern"},
			// La lecture champ à champ : quantité, unité, partitif, aliment,
			// note, optionnel.
			&core.JSONField{Name: "reading"},
			&core.TextField{Name: "food"},
			&core.BoolField{Name: "resolved"},
			&core.TextField{Name: "category"},
			// Une liste de clés stables, et non une colonne par signal ni un
			// select fermé : la liste appartient au code qui la produit, et
			// une migration ne doit pas devenir le point de passage obligé de
			// son enrichissement — l'argument déjà écrit sur
			// import_urls.cause. Le filtre « au moins un signal » se fait par
			// json_array_length.
			&core.JSONField{Name: "signals"},
			// La provenance de la première occurrence rencontrée, et d'elle
			// seule : la forme étant dédupliquée, il ne s'agit pas de la liste
			// de ses provenances mais d'un exemple. recipe est renseignée
			// quand la source est la base de l'instance, line quand c'est un
			// corpus fourni ; l'autre reste vide.
			//
			// Facultative et sans cascade, comme import_urls.recipe : l'établi
			// lit le carnet, il ne peut pas l'abîmer. PocketBase supprime
			// l'enregistrement qui porte la relation, jamais celui qu'elle
			// vise, et un champ facultatif sans cascade est simplement vidé le
			// jour où la recette part.
			&core.RelationField{
				Name:         "recipe",
				CollectionId: recettes.Id,
				MaxSelect:    1,
			},
			&core.NumberField{Name: "line", OnlyInt: true},
		)
		// « Une ligne par forme distincte » écrit dans le schéma plutôt que
		// confié à la bonne volonté de l'appelant. La même forme dans deux
		// passes distinctes reste permise : c'est le cas normal d'une seconde
		// lecture du même corpus.
		formes.AddIndex("idx_analyses_formes_analysis_raw", true, "analysis, raw", "")
		if err := app.Save(formes); err != nil {
			return fmt.Errorf("analyses_formes : %w", err)
		}

		annotations := core.NewBaseCollection("analyses_annotations")
		annotations.Fields.Add(
			&core.RelationField{
				Name:          "analysis",
				CollectionId:  passes.Id,
				MaxSelect:     1,
				Required:      true,
				CascadeDelete: true,
			},
			// La cible « groupe » n'est pas un enregistrement, puisque
			// l'agrégé n'est pas stocké : elle se désigne par l'aliment
			// canonique. Une annotation de forme porte form ; une annotation
			// de groupe porte food seul.
			&core.RelationField{
				Name:          "form",
				CollectionId:  formes.Id,
				MaxSelect:     1,
				CascadeDelete: true,
			},
			&core.TextField{Name: "food"},
			// Deux champs séparés dès maintenant : l'un cite la ligne brute et
			// ne quitte jamais l'instance, l'autre porte le verdict sur la
			// règle ou sur l'entrée du lexique, et sera le seul envoyable un
			// jour vers patachoo.org. Les séparer plus tard demanderait une
			// migration.
			&core.TextField{Name: "local_note"},
			&core.TextField{Name: "shareable_note"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(annotations); err != nil {
			return fmt.Errorf("analyses_annotations : %w", err)
		}

		if _, err := app.DB().NewQuery(tableDeLIndexDesFormes).Execute(); err != nil {
			return fmt.Errorf("table analyses_formes_fts : %w", err)
		}
		for _, pose := range declencheursDeLIndexDesFormes {
			if _, err := app.DB().NewQuery(pose.sql).Execute(); err != nil {
				return fmt.Errorf("déclencheur %s : %w", pose.nom, err)
			}
		}

		// Rien à réindexer, contrairement à recipes_fts : les trois
		// collections naissent vides.
		//
		// Les cinq règles des trois collections restent nulles, donc réservées
		// au superutilisateur par l'API REST. Ce n'est pas un oubli : les
		// pages de l'établi lisent côté serveur par e.App, qui ne passe pas
		// par les règles, et ce qui garde ces pages est le droit porté par
		// users (PATA-128) — un superutilisateur n'a pas de session sur le
		// site. Les deux couches ne se remplacent pas : l'API reste fermée
		// même à un compte portant le droit.
		return nil
	}, func(app core.App) error {
		// Les déclencheurs d'abord : ils écrivent dans la table virtuelle, que
		// la suite supprime.
		for _, pose := range declencheursDeLIndexDesFormes {
			if _, err := app.DB().NewQuery("DROP TRIGGER IF EXISTS " + pose.nom).Execute(); err != nil {
				return fmt.Errorf("suppression du déclencheur %s : %w", pose.nom, err)
			}
		}
		// Les tables d'ombre de FTS5 partent avec la table virtuelle.
		if _, err := app.DB().NewQuery("DROP TABLE IF EXISTS analyses_formes_fts").Execute(); err != nil {
			return fmt.Errorf("suppression de analyses_formes_fts : %w", err)
		}
		for _, nom := range collectionsDeLEtabliDuPlusJeuneAuPlusVieux {
			collection, err := app.FindCollectionByNameOrId(nom)
			if err != nil {
				continue // déjà absente : rien à défaire
			}
			if err := app.Delete(collection); err != nil {
				return fmt.Errorf("suppression de %s : %w", nom, err)
			}
		}
		return nil
	})
}
