# Journal des versions

**Une section par version, la plus récente en haut.** « À paraître » recueille au
fil de l'eau ce que chaque tâche livrée change : c'est ce qui permet de savoir ce
qui bouge d'une version à l'autre sans parcourir l'historique git.

Les numéros suivent [SemVer](https://semver.org/lang/fr/) et vivent dans le
dépôt sous forme de **tag git annoté, préfixé** — `v0.1.0`. Il n'y a pas de
fichier `VERSION` à la racine : il doublerait le tag, et les deux finiraient par
diverger.

## À paraître

- Le binaire porte sa version. `patachoo version` et `patachoo --version`
  l'impriment, et le pied de page l'affiche à un compte connecté, en lien vers
  les versions publiées. Construit sans `-ldflags`, il annonce ce qu'il est —
  `dev (1de2cd1, modifié)` — plutôt que de se taire.
