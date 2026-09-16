- Les deux workflows de la forge n'exécutent plus que des actions épinglées par
  empreinte de commit, la version exacte gardée en commentaire sur la même
  ligne. Ce que la CI exécute est donc décidé par le dépôt : un tag comme `v4`
  est redéployé par son mainteneur à chaque version mineure, et changeait sous
  ce nom sans qu'une ligne bouge ici — sur la vérification comme sur la
  publication de l'image, celle qui signe une attestation de provenance. Aucune
  action ne change de lignée majeure au passage : on fige ce qui tournait déjà.
  L'en-tête de `.github/workflows/verifie.yml` porte la convention et la
  commande qui remonte une empreinte le jour où l'on veut monter de version.
