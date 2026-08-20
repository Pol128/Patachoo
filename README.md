# Patachoo

Gestionnaire de recettes auto-hébergé : on colle l'URL d'une recette, on obtient
une fiche propre dans son propre carnet — ingrédients analysés, quantités
comprises, source d'origine citée.

> **Le gestionnaire de recettes qui comprend le français, et qui tourne en 40 Mo sur un Raspberry Pi.**

**En construction.** Le squelette démarre, le reste s'écrit.

## Démarrer

```sh
go run . serve                      # http://127.0.0.1:8090
go run . superuser upsert vous@exemple.fr 'motdepasse'
```

L'interface d'administration répond sur `/_/`, l'API REST sur `/api/`.

```sh
go test ./...                       # les tests, avant tout le reste
./verifie                           # gofmt, vet, tests, govulncheck
go build -o Patachoo .              # un binaire, rien d'autre à installer
```

Ce qu'il faut avoir fait pour dire qu'une tâche est terminée est écrit dans
[DOD.md](DOD.md) — tests unitaires, tests de sécurité, et la règle qui remplace
un seuil de couverture.

## Ce que ça sait faire que les autres ne savent pas

Lire une ligne d'ingrédient française. C'est un créneau vide, et ça se mesure :
huit lignes françaises typiques passées aux deux gestionnaires de recettes
auto-hébergés de référence en ressortent **sept fausses sur huit**, chez
**Mealie** (v3.22.0) comme chez **Tandoor**.

Ce que rend l'analyseur de Tandoor — 319 lignes d'expressions régulières :

```
"2 cuillères à soupe de crème fraîche"
    quantité 2   unité « cuillères »   aliment « à soupe de crème fraîche »
"une pincée de sel"
    quantité 0   unité vide            aliment « une pincée de sel »
"500 g de pommes de terre"
    quantité 500 unité « g »           aliment « de pommes de terre »
```

Mealie échoue autrement, pour le même résultat : son import n'appelle pas son
analyseur, et celui-ci déclare de toute façon l'anglais pour seule langue
reconnue — sur une bibliothèque de 1371 lignes importées, 36 étaient rattachées
à un aliment.

La cause tient en une phrase : leur modèle est **positionnel** — quantité au
premier mot, unité au deuxième, aliment dans tout le reste — et le français le
casse deux fois, par la **préposition partitive** (`de`, `d'`, `du`, `des`) qui
sépare l'unité de l'aliment et atterrit dans son nom, et par les **unités
multi-mots** comme `cuillère à soupe`, dont un modèle positionnel n'attrape
qu'un mot sur quatre.

Le retournement est là : cette grammaire est *plus régulière* que l'anglaise.
`2 cups sifted flour` n'a aucun marqueur de frontière, `2 cuillères à soupe de
farine tamisée` en a un, explicite. Traité comme du français, le problème
redevient déterministe.

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

## Ajouter une langue

Tout se passe dans [`github.com/Pol128/moteur`](https://github.com/Pol128/moteur),
pas ici : un pack de langue est un fichier `lang/<code>.toml`, accompagné s'il y
a lieu d'un lexique d'aliments `data/foods_<code>.json`. Les deux sont embarqués
dans le binaire par `go:embed` — il n'y a rien à déposer à côté à l'exécution.

**Aucune règle de langue n'est écrite en Go.** Unités, partitifs, fractions,
seuils de pluriel, formes irrégulières : le code applique ce que le pack
déclare, il ne connaît pas la langue. Ce n'est pas une intention, c'est testé —
`TestPackFactice` fait tourner l'analyseur sur une langue inventée, déclarée
uniquement par son pack, et vérifie qu'il la lit.

Un contributeur espagnol écrit donc `lang/es.toml` et ouvre sa MR sur `moteur`.
Patachoo n'a pas de point de réglage à offrir de son côté : il consomme le
module, et hérite des langues que le module sait lire.

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
