- L'installation ne conseille plus d'écrire le mot de passe d'administration sur
  la ligne de commande. `INSTALL.md` met en premier l'URL affichée dans les logs
  au premier démarrage, qui crée le compte depuis le navigateur ; les commandes
  `superuser upsert` qui restent, là et dans le `README`, disent ce qu'un
  argument laisse voir dans `ps` et dans l'historique du shell, et comment s'en
  prémunir ; les blocs `curl` de sauvegarde, de restauration et de répétition
  lisent désormais le mot de passe au clavier.
