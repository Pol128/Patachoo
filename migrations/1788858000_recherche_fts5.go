package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// L'index de recherche du carnet : une table virtuelle FTS5 tokenisée par
// unicode61, qui replie les accents et cherche par préfixe. C'est ce que le
// LIKE de SQLite ne sait pas faire — « Crème » LIKE '%creme%' est faux, et en
// français on tape volontiers sans accents.
//
// Une ligne par recette, et non une par ingrédient : c'est ce qui supprime à la
// source le doublon que la jointure sur les lignes obligeait à éviter à la main.
//
// recipe_id porte l'identifiant texte de la recette : les identifiants
// PocketBase sont des chaînes de 15 caractères, ils ne peuvent pas servir de
// rowid. UNINDEXED pour qu'ils ne soient pas cherchables.
//
// Une migration nouvelle, et non le schéma initial retouché : une base déjà
// installée n'appliquerait pas une migration qu'elle a déjà passée, et se
// retrouverait sans index.
const tableDeLIndex = `CREATE VIRTUAL TABLE recipes_fts USING fts5(
	recipe_id UNINDEXED,
	title,
	ingredients,
	tags,
	tokenize = "unicode61 remove_diacritics 2"
)`

// declencheur : l'ordre de création, et le nom qu'il faudra pour le défaire.
// Les deux ensemble plutôt que deux listes qui finiraient par diverger — un
// down qui oublie un déclencheur ne le dit pas.
type declencheur struct {
	nom string
	sql string
}

// declencheursDeLIndex : la tenue à jour de l'index, par déclencheurs SQL
// plutôt que par des hooks PocketBase.
//
// Le déclencheur écrit dans la transaction de l'écriture : l'index ne peut pas
// diverger de la base sur une erreur intermédiaire, là où un hook qui échoue
// après le commit laisserait un index faux que rien ne signale. Et il vaut pour
// tout chemin d'écriture — hooks, interface d'administration, sous-commande
// migrate, correction SQL à la main, et le prochain code qui oubliera d'appeler
// le hook.
//
// Le risque qu'on pourrait leur opposer n'existe pas ici : PocketBase ne
// reconstruit jamais la table d'une collection quand elle change.
// SyncRecordTableSchema ne fait que des ALTER TABLE, et SQLite répercute un
// renommage de colonne dans le corps des déclencheurs.
var declencheursDeLIndex = []declencheur{
	// À la création, la recette n'a pas encore de ligne d'ingrédient : ce sont
	// les déclencheurs d'ingredients qui rempliront la colonne.
	{nom: "recipes_fts_apres_insertion_recette", sql: `CREATE TRIGGER recipes_fts_apres_insertion_recette AFTER INSERT ON recipes BEGIN
		INSERT INTO recipes_fts(recipe_id, title, ingredients, tags)
		VALUES (new.id, new.title, ` + lesIngredientsDe("new.id") + `, ` + lesTagsDe("new.id") + `);
	END`},

	// Le titre et les tags, et eux seuls : les lignes d'ingrédients ne vivent
	// pas dans cette table et ne changent pas en même temps qu'elle.
	{nom: "recipes_fts_apres_maj_recette", sql: `CREATE TRIGGER recipes_fts_apres_maj_recette AFTER UPDATE ON recipes BEGIN
		UPDATE recipes_fts
		SET title = new.title, tags = ` + lesTagsDe("new.id") + `
		WHERE recipe_id = new.id;
	END`},

	{nom: "recipes_fts_apres_suppression_recette", sql: `CREATE TRIGGER recipes_fts_apres_suppression_recette AFTER DELETE ON recipes BEGIN
		DELETE FROM recipes_fts WHERE recipe_id = old.id;
	END`},

	{nom: "recipes_fts_apres_insertion_ingredient", sql: `CREATE TRIGGER recipes_fts_apres_insertion_ingredient AFTER INSERT ON ingredients BEGIN
		UPDATE recipes_fts
		SET ingredients = ` + lesIngredientsDe("new.recipe") + `
		WHERE recipe_id = new.recipe;
	END`},

	// Deux mises à jour, et non une : une ligne peut changer de recette, et les
	// deux colonnes sont alors à recalculer.
	{nom: "recipes_fts_apres_maj_ingredient", sql: `CREATE TRIGGER recipes_fts_apres_maj_ingredient AFTER UPDATE ON ingredients BEGIN
		UPDATE recipes_fts
		SET ingredients = ` + lesIngredientsDe("old.recipe") + `
		WHERE recipe_id = old.recipe;
		UPDATE recipes_fts
		SET ingredients = ` + lesIngredientsDe("new.recipe") + `
		WHERE recipe_id = new.recipe;
	END`},

	{nom: "recipes_fts_apres_suppression_ingredient", sql: `CREATE TRIGGER recipes_fts_apres_suppression_ingredient AFTER DELETE ON ingredients BEGIN
		UPDATE recipes_fts
		SET ingredients = ` + lesIngredientsDe("old.recipe") + `
		WHERE recipe_id = old.recipe;
	END`},

	// Le seul déclencheur nécessaire sur tags. La suppression d'un tag n'en
	// demande pas : PocketBase retire son identifiant des recettes qui le
	// référencent et les enregistre, ce qui passe par la mise à jour des
	// recettes. Même chose pour la fusion de tags, qui passe par app.Save.
	{nom: "recipes_fts_apres_maj_tag", sql: `CREATE TRIGGER recipes_fts_apres_maj_tag AFTER UPDATE OF name ON tags BEGIN
		UPDATE recipes_fts
		SET tags = ` + lesTagsDe("recipes_fts.recipe_id") + `
		WHERE recipe_id IN (
			SELECT r.id FROM recipes r, json_each(r.tags) je WHERE je.value = new.id
		);
	END`},
}

// lesIngredientsDe et lesTagsDe rendent les deux sous-requêtes qui
// dénormalisent une recette : ses lignes brutes recollées d'un côté, les noms
// de ses tags de l'autre.
//
// L'argument est une expression SQL écrite ici même — « new.id », « old.recipe »,
// « recipes_fts.recipe_id ». Rien qui vienne d'une saisie n'entre par là : ces
// chaînes sont assemblées une fois, au démarrage, pour construire des
// déclencheurs figés.
func lesIngredientsDe(recette string) string {
	return `(SELECT coalesce(group_concat(i.raw, ' '), '')
		FROM ingredients i WHERE i.recipe = ` + recette + `)`
}

// recipes.tags est une colonne JSON DEFAULT '[]' NOT NULL : json_each s'y
// applique sans garde de nullité.
func lesTagsDe(recette string) string {
	return `(SELECT coalesce(group_concat(t.name, ' '), '')
		FROM tags t
		WHERE t.id IN (
			SELECT je.value FROM recipes r, json_each(r.tags) je WHERE r.id = ` + recette + `
		))`
}

func init() {
	m.Register(func(app core.App) error {
		if _, err := app.DB().NewQuery(tableDeLIndex).Execute(); err != nil {
			return fmt.Errorf("table recipes_fts : %w", err)
		}
		for _, pose := range declencheursDeLIndex {
			if _, err := app.DB().NewQuery(pose.sql).Execute(); err != nil {
				return fmt.Errorf("déclencheur %s : %w", pose.nom, err)
			}
		}
		// Les recettes déjà en base doivent entrer dans l'index : sans ça,
		// elles disparaissent de la recherche le jour de la mise à jour.
		return reindexeLaRecherche(app)
	}, func(app core.App) error {
		for _, pose := range declencheursDeLIndex {
			if _, err := app.DB().NewQuery("DROP TRIGGER IF EXISTS " + pose.nom).Execute(); err != nil {
				return fmt.Errorf("suppression du déclencheur %s : %w", pose.nom, err)
			}
		}
		// Les tables d'ombre de FTS5 partent avec la table virtuelle ; il n'y a
		// rien d'autre à défaire.
		if _, err := app.DB().NewQuery("DROP TABLE IF EXISTS recipes_fts").Execute(); err != nil {
			return fmt.Errorf("suppression de recipes_fts : %w", err)
		}
		return nil
	})
}

// reindexeLaRecherche reconstruit l'index entier depuis les trois sources.
//
// Une fonction nommée, appelée par la migration, plutôt que le corps de
// m.Register : c'est ce qui la rend exerçable par un test. Une réindexation
// qu'on ne peut pas jouer est une réindexation qu'on découvre cassée en
// production.
func reindexeLaRecherche(app core.App) error {
	if _, err := app.DB().NewQuery("DELETE FROM recipes_fts").Execute(); err != nil {
		return fmt.Errorf("vidage de l'index : %w", err)
	}

	remplissage := `INSERT INTO recipes_fts(recipe_id, title, ingredients, tags)
		SELECT r.id, r.title, ` + lesIngredientsDe("r.id") + `, ` + lesTagsDe("r.id") + `
		FROM recipes r`
	if _, err := app.DB().NewQuery(remplissage).Execute(); err != nil {
		return fmt.Errorf("remplissage de l'index : %w", err)
	}
	return nil
}
