- `patachoo analyse comparer` dit ce que la montée du parser change sur un
  corpus, sans avoir besoin d'une vérité de référence : entre deux analyses du
  même corpus, ou entre les colonnes déjà écrites dans les ingrédients et une
  analyse, ce qui est la seule façon de comparer à une version du parser qui ne
  se rejoue plus. Le rapprochement se fait sur la ligne brute, qui ne bouge
  jamais. La sortie donne de part et d'autre le nombre de formes qu'aucun signal
  de relecture n'allume, le taux d'aliments reconnus, le compte de chaque signal
  et de chaque motif de lecture, avec leur écart ; puis les formes qui ont
  changé, rangées par famille — celles dont seul l'aliment prend sa forme
  canonique, tenues hors du verdict, celles qui n'allumaient aucun signal et en
  allument désormais, listées en entier pour être relues, et le reste. Rien
  n'est écrit dans les recettes ni dans leurs ingrédients. `analyse resume`
  gagne au passage le compte des formes sans aucun signal.
