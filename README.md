# Patachoo

Gestionnaire de recettes auto-hébergé : on colle l'URL d'une recette, on obtient
une fiche propre dans son propre carnet — ingrédients analysés, quantités
comprises, source d'origine citée.

> **Le gestionnaire de recettes qui comprend le français, et qui tourne en 40 Mo sur un Raspberry Pi.**

**En construction.** Le squelette démarre, le reste s'écrit.

## Démarrer

Deux chemins, à ne pas confondre. Pour **installer une instance**, tout est
dans [le guide d'installation : image Docker, `docker-compose.yml`, adresse et
port, TLS, sauvegarde et restauration](INSTALL.md). Les commandes ci-dessous
sont l'autre chemin — **faire tourner le dépôt depuis les sources**.

```sh
go run ./cmd/patachoo serve         # http://127.0.0.1:8090
go run ./cmd/patachoo superuser upsert vous@exemple.fr 'motdepasse'
```

Ce mot de passe est un argument de ligne de commande : `ps` le montre à tout
utilisateur de la machine le temps de la commande, et il reste ensuite en clair
dans l'historique du shell. Au premier démarrage, les logs affichent une URL qui
crée ce compte depuis le navigateur, sans rien écrire sur la ligne de commande —
voir [« Créer le premier
superutilisateur »](INSTALL.md#créer-le-premier-superutilisateur) dans
`INSTALL.md`, qui donne aussi les remèdes quand la commande reste nécessaire.

L'interface d'administration répond sur `/_/`, l'API REST sur `/api/` — sur le
**même port que le site**, donc sur les mêmes interfaces que lui. Ni l'une ni
l'autre ne doit être joignable depuis l'Internet sans filtre devant : voir
[Exposer Patachoo hors de chez soi](INSTALL.md#exposer-patachoo-hors-de-chez-soi).

```sh
go test ./...                       # les tests, avant tout le reste
./verifie                           # gofmt, vet, tests, govulncheck — ~2 min
AVEC_RACE=1 ./verifie               # la même chose avec -race — ~20 min
go build -o Patachoo ./cmd/patachoo # un binaire, rien d'autre à installer
```

`./verifie` nu est la boucle courte, celle qu'on lance à chaque geste.
`AVEC_RACE=1 ./verifie` ajoute le détecteur de courses de Go : c'est la passe
que la CI lance à chaque poussée, et celle à lancer soi-même avant d'ouvrir une
demande de fusion qui touche à du code concurrent. Elle dure une vingtaine de
minutes — le détecteur multiplie par dix la durée d'un paquet qui monte une
base PocketBase, et le paquet `cmd/patachoo` en monte une par test. Ce n'est
pas qu'elle est bloquée.

Ce qu'il faut avoir fait pour dire qu'une tâche est terminée est écrit dans
[DOD.md](DOD.md) — tests unitaires, tests de sécurité, et la règle qui remplace
un seuil de couverture.

Une faille se signale **en privé**, jamais par une issue publique :
[SECURITY.md](SECURITY.md) dit par où, ce qu'un bon signalement contient, et ce
qu'il faut en attendre.

## Ouvrir ou fermer l'inscription

Une instance neuve s'installe **porte fermée** : personne ne peut créer de
compte, et `/inscription` répond 404. Le superuser existe déjà — il est créé en
ligne de commande — et c'est lui qui ouvre, quand il le décide.

Le réglage se bascule dans l'administration sur `/_/` : collection `settings`,
l'unique enregistrement, case `open_registration`, puis *Save*. Il est relu à
chaque requête, donc **rien à redémarrer**.

| `open_registration` | Ce que ça change |
| --- | --- |
| décoché (défaut) | `/inscription` répond 404, aucun compte ne se crée, et le lien vers cette page disparaît de `/connexion` |
| coché | `/inscription` sert son formulaire, l'inscrit est connecté dans la foulée, et `/connexion` porte le lien vers elle |

L'enregistrement `settings` effacé vaut porte fermée : le défaut d'un réglage
de sécurité se choisit du côté qui refuse.

Dans les deux états, `POST /api/collections/users/records` reste refusé —
`users.createRule` est verrouillée au superuser. L'inscription n'a donc qu'une
porte, la nôtre. Un compte créé sans passer par elle se crée depuis `/_/`, où
le superuser édite `users` directement.

Les deux chemins pour ouvrir un compte à quelqu'un — le créer soi-même depuis
`/_/`, ou ouvrir l'inscription puis la refermer — sont détaillés dans [« Créer
un compte ordinaire »](INSTALL.md#créer-un-compte-ordinaire) dans `INSTALL.md`.

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

## Versions

Les numéros suivent [SemVer](https://semver.org/lang/fr/) — `MAJEUR.MINEUR.CORRECTIF` —
et vivent dans le dépôt sous forme de **tag git annoté, préfixé** : `v0.1.0`.
Pas de fichier `VERSION` à la racine, qui doublerait le tag et finirait par en
diverger. Ce qui change d'une version à l'autre est dans
[CHANGELOG.md](CHANGELOG.md).

Où lire la version d'une instance :

```sh
patachoo version                    # imprime la version et sort
docker compose exec patachoo /patachoo version
```

Le **pied de page** l'affiche aussi, à un compte connecté seulement, en lien
vers les versions publiées : c'est une information d'exploitation, utile à qui
administre l'instance, pas à qui frappe à la porte.

Un binaire construit à la main n'est estampillé par aucun tag : il annonce alors
`dev`, complété par la révision git et l'état de l'arbre au moment du build —
`dev (1de2cd1, modifié)`. Une version publiée, elle, est construite avec
`-ldflags "-X main.version=0.1.0"` ; l'image Docker la reçoit par
`--build-arg VERSION=0.1.0`, qui alimente d'un seul geste le binaire et
l'étiquette OCI de l'image.

Les versions publiées sont sur
[github.com/Pol128/Patachoo/releases](https://github.com/Pol128/Patachoo/releases).

**Patachoo ne va jamais vérifier s'il est à jour.** L'instance n'appelle
personne — ni au démarrage, ni périodiquement, ni derrière un bouton : elle
affiche ce qu'elle est et pointe où lire le reste. Le produit doit fonctionner
sur un réseau coupé d'Internet, et une instance auto-hébergée n'a pas à se
signaler à un tiers pour tourner.

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
