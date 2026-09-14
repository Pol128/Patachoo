# Journal des versions

**Une section par version, la plus récente en haut.** « À paraître » recueille au
fil de l'eau ce que chaque tâche livrée change : c'est ce qui permet de savoir ce
qui bouge d'une version à l'autre sans parcourir l'historique git.

Les numéros suivent [SemVer](https://semver.org/lang/fr/) et vivent dans le
dépôt sous forme de **tag git annoté, préfixé** — `v0.1.0`. Il n'y a pas de
fichier `VERSION` à la racine : il doublerait le tag, et les deux finiraient par
diverger.

## À paraître

- Les réponses du serveur portent `Referrer-Policy: no-referrer`. L'adresse
  de l'instance ne part plus vers les sites tiers qu'une page contacte —
  l'aperçu de l'image chez le site importé, le lien du pied de page. Pour une
  installation auto-hébergée sur un domaine privé, c'est son existence qui
  cesse d'être annoncée. En contrepartie, l'aperçu d'une image distante peut
  ne plus s'afficher chez les sites qui refusent une requête sans `Referer`.
- Les données ne sont plus lisibles par les autres comptes de la machine. Le
  serveur crée `pb_data`, ses bases et ses sauvegardes en `0700` et `0600` :
  `data.db` porte en clair de quoi fabriquer un jeton d'administration, et
  n'importe quel compte local pouvait le lire. Une instance déjà installée garde
  les droits de ses fichiers existants — `INSTALL.md` donne la commande de
  rattrapage.
- Le lancement d'un import en lot est plafonné : cinq fournées par minute et par
  adresse. Au-delà, la page de saisie revient avec un message et la liste collée
  encore dans le champ, au lieu du JSON brut d'une erreur d'API. Et une minute
  dont tous les noms de fournée sont déjà pris n'est plus une panne : elle rend
  la même page et invite à réessayer, là où elle rendait une erreur.
- La création de comptes est plafonnée. Une même adresse dispose de dix
  inscriptions par heure ; au-delà, la page d'inscription revient sous un
  « Trop de tentatives d'inscription », sans qu'aucun compte de plus soit créé.
  Une instance qui garde son inscription fermée continue de répondre comme
  avant. Le plafond de la page de connexion, lui, ne bouge pas.
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
