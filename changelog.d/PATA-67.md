- L'import refuse désormais les adresses d'un tailnet ou d'un réseau CGNAT, au
  même titre que les adresses privées. Une installation posée sur un tailnet —
  Tailscale numérote ses pairs dans la plage partagée `100.64.0.0/10` — ne peut
  plus servir à en explorer les machines depuis une URL collée dans l'import.
  Trois plages réservées voisines sont refusées avec elle : `0.0.0.0/8`,
  `198.18.0.0/15` et `240.0.0.0/4`.
