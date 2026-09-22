- La section d'`INSTALL.md` qui explique comment vérifier la provenance de
  l'image dit maintenant quelle version de `gh` la commande réclame — 2.49 au
  minimum —, comment lire la sienne, et ce que rend un `gh` plus ancien : un
  `unknown command`, qui ressemble à une coquille dans la page sans en être
  une. Pour qui ne peut pas mettre `gh` à jour, elle donne une voie de repli
  avec `curl` et `jq` seuls, qui lit l'attestation directement dans le
  registre et montre ce qu'elle affirme. Cette voie dit aussi, sans détour, ce
  qu'elle ne fait pas : elle ne vérifie aucune signature.
