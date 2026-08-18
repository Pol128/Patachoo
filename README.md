# Patachoo

Gestionnaire de recettes auto-hébergé : on colle l'URL d'une recette, on obtient
une fiche propre dans son propre carnet — ingrédients analysés, quantités
comprises, source d'origine citée.

**En construction.** Le squelette démarre, le reste s'écrit.

## Démarrer

```sh
go run . serve                      # http://127.0.0.1:8090
go run . superuser upsert vous@exemple.fr 'motdepasse'
```

L'interface d'administration répond sur `/_/`, l'API REST sur `/api/`.

```sh
go test ./...                       # les tests, avant tout le reste
go build -o Patachoo .              # un binaire, rien d'autre à installer
```

## Ce que c'est, techniquement

- **Go**, avec **PocketBase comme bibliothèque** — pas comme exécutable tout
  fait. SQLite, migrations, authentification, règles d'accès et interface
  d'administration sont fournis ; nous écrivons l'import, le rendu et le
  branchement du parser. Le tout part dans **un binaire unique**.
- **Rendu serveur**, `html/template` + **HTMX**. Pas de SPA, pas de Node, pas
  d'étape de build.
- **Import par URL** : lecture du **JSON-LD schema.org** publié par les sites.
  Mesuré sur 206 recettes réparties sur 68 domaines : 71,8 % exploitables.
- L'analyse des lignes d'ingrédients vit dans un module séparé,
  [`github.com/Pol128/moteur`](https://github.com/Pol128/moteur), réutilisable
  hors de Patachoo.

## Licence

**Apache-2.0** — voir [LICENSE](LICENSE) et [NOTICE](NOTICE).

Trois conséquences pratiques, valables pour toute contribution :

1. **Pas d'en-tête de licence dans les fichiers source.** Apache-2.0 le
   recommande sans l'imposer ; on a tranché une fois pour toutes : `LICENSE` et
   `NOTICE` à la racine suffisent. Ne pas en ajouter fichier par fichier.
2. **`NOTICE` se met à jour avec le code.** Tout composant tiers redistribué —
   y compris des données — s'y déclare avec sa licence. Apache-2.0 §4(d) impose
   que ce fichier soit propagé par ceux qui redistribuent Patachoo.
3. **Aucune ligne venant de Mealie ou Tandoor**, tous deux en AGPL-3.0. On peut
   lire leur analyseur pour comprendre pourquoi il échoue sur le français — c'est
   fait et documenté — mais en recopier un extrait basculerait tout Patachoo sous
   AGPL-3.0, et l'absorption ne se défait pas.
