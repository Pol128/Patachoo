- Patachoo sait désormais passer un corpus entier de lignes d'ingrédients au
  parser, hors de toute page : les lignes de la base de l'instance, ou un
  fichier du disque de la machine. Le travail dédoublonne les lignes en formes
  distinctes, compte leurs occurrences, garde la lecture du parser et les
  signaux de relecture de chacune. Rien n'est écrit dans les recettes ni dans
  leurs ingrédients : l'établi lit le carnet, il ne le modifie pas. Une analyse
  à la fois, et trois garde-fous qui refusent un corpus hors de proportion —
  500 000 lignes, 32 Mio, trente minutes. Deux commandes le pilotent en
  attendant ses écrans : `patachoo analyse lancer [fichier]` et
  `patachoo analyse resume <identifiant>`, qui rend le compte des formes, des
  signaux, des aliments reconnus ou non, et leur répartition par catégorie.
