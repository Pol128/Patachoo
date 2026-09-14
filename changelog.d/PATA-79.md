- La connexion et l'inscription ne lisent plus leurs champs que dans le corps
  du formulaire. Un mot de passe placé dans l'adresse — `?courriel=…&mot-de-passe=…` —
  n'ouvre plus de session et ne crée plus de compte : il cessait d'être un
  secret dès la première requête, l'adresse complète étant recopiée dans les
  journaux du serveur, dans les sauvegardes de la nuit et dans l'historique du
  navigateur.
