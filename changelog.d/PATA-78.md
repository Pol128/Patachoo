- `INSTALL.md` explique comment servir Patachoo en HTTPS hors de la machine
  locale — proxy inverse, certificat obtenu par Patachoo lui-même, ou simple
  tunnel SSH — et nomme le symptôme d'un accès en clair depuis un autre poste :
  la connexion est acceptée mais reste sans effet, parce que le navigateur jette
  un cookie `Secure` venu d'une origine qui ne l'est pas. Rien ne change dans le
  produit ; c'est la documentation qui décrivait un déploiement impossible.
