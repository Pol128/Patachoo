- L'import refuse les adresses privées écrites en IPv6. Une URL qui enferme
  une IPv4 interne dans une écriture NAT64 (`64:ff9b::/96`), 6to4
  (`2002::/16`) ou compatible v4 (`::/96`) — par exemple
  `http://[64:ff9b::a9fe:a9fe]/` pour 169.254.169.254 — est désormais refusée
  comme l'est déjà sa forme v4. Les adresses publiques écrites sous ces mêmes
  formes restent importables.
