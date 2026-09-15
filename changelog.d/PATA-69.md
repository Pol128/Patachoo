- Une grosse fournée d'import ne laisse plus s'accumuler des connexions
  sortantes inactives. Chaque récupération de page fermait les siennes au bout
  de quatre-vingt-dix secondes seulement : un lot de cinq cents adresses pouvait
  ainsi épuiser les descripteurs de fichiers du serveur, qui cessait alors de
  répondre à tout le monde, sans rien écrire dans les journaux. Elles sont
  désormais fermées dès la page récupérée.
