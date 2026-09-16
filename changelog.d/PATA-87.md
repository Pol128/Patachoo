- `./verifie` ne télécharge plus la dernière version publiée de `govulncheck` au
  moment où il tourne : il en exécute une version écrite en toutes lettres dans
  le script, `v1.8.0`. Ce que la vérification exécute est donc décidé par le
  dépôt et lisible sans rien lancer, là où `@latest` laissait le proxy de
  modules choisir — sur la machine de développement comme sur le runner de CI,
  à chaque poussée. L'analyse ne perd rien en fraîcheur : la base de
  vulnérabilités est interrogée sur `vuln.go.dev` à l'exécution, indépendamment
  de la version de l'outil. Le module reste hors de `go.mod` ; le script dit en
  commentaire comment remonter ce numéro le jour venu.
