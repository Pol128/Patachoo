- L'image Docker est construite et publiée par la forge, sur tag de version, et
  porte une attestation de provenance qui la relie au commit dont elle sort.
  Chaque version publie ses références `0.1.0` et `0.1` à côté de `latest` ;
  `INSTALL.md` dit comment épingler l'image par empreinte pour décider soi-même
  quand on monte de version, et comment vérifier d'une commande que celle qu'on
  a tirée vient bien de ce dépôt.
