- Pour qui contribue : la passe `AVEC_RACE=1 ./verifie` tient désormais en une
  douzaine de minutes, et la courte en deux. Les tests de `cmd/patachoo` ne
  migrent plus chacun leur base : ils recopient une base migrée une fois par
  passe, et chacun garde la sienne. Le `-timeout` de la passe longue redescend
  de trois heures à une.
