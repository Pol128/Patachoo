- La documentation dit comment exposer Patachoo hors de chez soi : une section
  d'`INSTALL.md` donne le proxy inverse en HTTPS, la variante du compose qui
  l'accompagne, et la façon de fermer `/_/` de l'extérieur. Le tableau des
  chemins et le `README` avertissent désormais que `/_/` et `/api/` répondent
  sur le même port que le carnet et ne doivent pas être publiés sans filtre
  devant. Le chiffrement des réglages est aussi documenté pour Docker.
