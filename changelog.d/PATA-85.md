- `INSTALL.md` explique le réglage à faire derrière un proxy inverse. Sans lui,
  le plafond de cinq tentatives de connexion par minute compte tous les
  visiteurs sur un seul compteur, et « trop de tentatives » finit par être
  opposé à des gens qui n'ont rien tenté. La section dit quel en-tête déclarer
  dans `/_/`, laquelle des adresses est retenue, pourquoi ne jamais le déclarer
  sans proxy, et comment vérifier le résultat.
