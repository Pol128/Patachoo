- Le cookie de session n'ouvre plus que les pages du produit. Il
  n'authentifie plus les adresses `/api/` ni `/_/` : une faille d'affichage
  dans une page n'y gagne donc plus l'API REST du compte connecté, avec ses
  collections entières et ses opérations de compte. Rien ne change à l'usage —
  les pages, les formulaires et les vignettes des recettes se comportent comme
  avant — et un client d'API qui porte son propre jeton reste servi.
