- L'authentification par l'API est plafonnée comme la page de connexion : cinq
  tentatives par minute et par adresse sur
  `POST /api/collections/users/auth-with-password`, là où la règle livrée par
  PocketBase en laissait quarante. La porte d'à côté gardait son plafond, celle-ci
  ne l'avait pas. L'authentification d'administration, elle, ne bouge pas.
