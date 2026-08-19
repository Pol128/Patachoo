# Le corpus de test de l'extracteur

**Contenu inventé, structures réelles.** Aucune page de ce répertoire n'a été
récoltée sur un site : les recettes n'existent nulle part, le site
« Les Fourneaux de Perlimpinpin » non plus. Ce qui est fidèle, c'est le
*balisage* — chaque forme reproduite ici a été rencontrée pour de vrai sur les
206 recettes mesurées (68 domaines).

## Pourquoi de toutes pièces

Le cache du récolteur est local et gitignoré. Un test qui en dépend n'est
vérifiable ni en intégration continue, ni par quelqu'un qui vient de cloner le
dépôt. Un corpus écrit est versionné, lisible en revue, stable — aucun site ne
peut le casser en refondant ses pages — et il évacue la question du droit des
bases de données.

**Ce qu'il ne prouve pas :** qu'un site donné passe aujourd'hui. La parade n'est
pas dans les tests automatiques mais dans une vérification manuelle
occasionnelle contre le cache local. Et le jour où un vrai site échoue, on en
tire un nouveau cas fictif — c'est ce qui fait grandir le corpus au lieu de le
figer.

## Un cas = deux fichiers

```
nom-du-cas.html            la page, avec son balisage
nom-du-cas.attendu.json    ce que l'extraction doit en tirer
```

Le fichier attendu porte trois clés :

- `cas` — ce que ce fichier prouve, en une phrase. Un cas dont on ne sait plus
  ce qu'il démontre finit supprimé au premier ménage ; le test refuse un `cas`
  vide.
- `erreur` — vide, ou l'un des échecs nommés du paquet (`sans_recette`,
  `aucun_balisage`, `json_invalide`, `titre_absent`).
- `recette` — le résultat attendu, ou `null` si `erreur` est renseignée. Jamais
  les deux, jamais aucun des deux.

Ajouter un cas, c'est déposer ces deux fichiers et ajouter son nom à
`casObligatoires` dans `corpus_test.go`. Rien d'autre à toucher.

## Ce que le corpus fixe, au-delà des formes

Quelques décisions y sont inscrites en dur, parce qu'un fichier attendu est
l'endroit le moins ambigu pour les écrire :

- **`recipeInstructions` en chaîne unique** : les sauts de ligne séparent les
  étapes, les lignes vides sont ignorées.
- **`HowToSection`** : les sections sont aplaties, l'ordre des étapes conservé,
  le nom de section n'est pas retenu comme une étape.
- **`image` ou `recipeYield` en tableau** : on retient la première entrée, sans
  chercher la « meilleure ».
- **Durées ISO 8601** converties en minutes (`PT3H30M` → 210).
- **Entités HTML décodées** (`&amp;` → `&`). Les sites en publient dans leur
  JSON-LD ; l'utilisateur n'a pas à les lire brutes.
- **Une `Recipe` sans `name` échoue** plutôt que d'inventer un titre.

## L'état d'aujourd'hui

L'extraction elle-même est PATA-7 et n'existe pas encore. Les tests actuels
vérifient donc le corpus : que chaque cas annoncé est là, que les attentes sont
cohérentes, que les blocs `ld+json` sont du JSON valide — sauf `json-malforme`,
dont c'est justement le sujet — et qu'aucun fichier attendu ne traîne sans sa
page. Quand PATA-7 arrivera, le même parcours de répertoire appellera
l'extracteur et comparera à `attendu.json`.
