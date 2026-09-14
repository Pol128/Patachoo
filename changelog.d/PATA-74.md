- Le cookie de session change de nom et **ce déploiement déconnecte tout le
  monde une fois** : l'ancien cookie n'est plus lu, il faut se reconnecter. Le
  nouveau nom porte le préfixe `__Host-`, qui fait refuser par le navigateur
  tout cookie de session qu'un sous-domaine voisin — un `blog.exemple.fr` à
  côté d'un `patachoo.exemple.fr` — tenterait de poser à sa place. Rien à
  changer à l'installation.
