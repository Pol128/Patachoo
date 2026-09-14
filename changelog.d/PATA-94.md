- Les pages ne sont plus conservées par les caches. Le serveur répond
  `Cache-Control: private, no-store` sur ses pages et ses fragments : un proxy
  ou un CDN placé devant l'instance ne peut plus servir à un visiteur la page
  rendue pour le compte d'un autre. Le navigateur redemande donc la page au
  serveur lors d'une navigation arrière. La feuille de style, htmx et les
  illustrations restent mis en cache comme avant.
