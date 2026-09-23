- Le rythme d'au plus une requête par seconde vaut désormais par site, et non
  par nom d'hôte : les sous-domaines d'un même site — `a.exemple.fr`,
  `b.exemple.fr` — se partagent ce rythme, et le délai entre deux visites
  qu'en demande l'un vaut pour tous. Dans un import en lot, ils se suivent au
  lieu de partir ensemble, sans retenir les autres sites de la fournée. Deux
  sites hébergés sous le même nom de service — deux blogs sur un même
  hébergeur, par exemple — comptent désormais comme un seul, et une fournée qui
  les mêle est plus lente.
