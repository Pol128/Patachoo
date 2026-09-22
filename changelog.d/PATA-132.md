- La vérification automatique ne lance plus qu'une passe à la fois sur la
  machine qui l'héberge : les suivantes attendent leur tour au lieu de se
  partager les mêmes processeurs, et aucune n'est plus annulée pour faire de
  la place. Une demande de fusion peut donc attendre derrière une autre avant
  d'être vérifiée, mais chaque passe dure ce qu'elle doit durer.
