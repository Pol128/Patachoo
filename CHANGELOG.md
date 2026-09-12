# Journal des versions

**Une section par version, la plus récente en haut.** « À paraître » recueille au
fil de l'eau ce que chaque tâche livrée change : c'est ce qui permet de savoir ce
qui bouge d'une version à l'autre sans parcourir l'historique git.

Les numéros suivent [SemVer](https://semver.org/lang/fr/) et vivent dans le
dépôt sous forme de **tag git annoté, préfixé** — `v0.1.0`. Il n'y a pas de
fichier `VERSION` à la racine : il doublerait le tag, et les deux finiraient par
diverger.

## À paraître

- Les pages ne sont plus conservées par les caches. Le serveur répond
  `Cache-Control: private, no-store` sur ses pages et ses fragments : un proxy
  ou un CDN placé devant l'instance ne peut plus servir à un visiteur la page
  rendue pour le compte d'un autre. Le navigateur redemande donc la page au
  serveur lors d'une navigation arrière. La feuille de style, htmx et les
  illustrations restent mis en cache comme avant.
- La source d'une recette se saisit et se corrige. Le formulaire porte un champ
  « Source — adresse de la page d'origine », prérempli par l'import et visible à
  la création comme à l'édition : une recette tapée à la main ou importée à
  l'unité affiche désormais son origine, là où seules les fournées le
  faisaient. Changer l'adresse oublie le nom du site enregistré, qui désignait
  l'ancienne ; vider le champ retire la source.
- Le binaire porte sa version. `patachoo version` et `patachoo --version`
  l'impriment, et le pied de page l'affiche à un compte connecté, en lien vers
  les versions publiées. Construit sans `-ldflags`, il annonce ce qu'il est —
  `dev (1de2cd1, modifié)` — plutôt que de se taire.
- Les recettes importées en fournée arrivent illustrées. L'image que la page
  publie est téléchargée et attachée comme à l'import unitaire ; une image
  injoignable ou refusée ne coûte plus la recette, elle la laisse simplement
  sans illustration. Les fournées d'avant ne sont pas rattrapées.
- Les sources du programme vivent dans `cmd/patachoo/`, gabarits et assets
  compris : la racine du dépôt ne porte plus que ses fichiers d'accueil. Rien
  ne change pour qui installe le binaire ou l'image ; qui construit depuis les
  sources écrit désormais `go run ./cmd/patachoo serve` et
  `go build -o Patachoo ./cmd/patachoo`.
