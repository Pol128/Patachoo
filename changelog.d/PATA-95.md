- Les réponses du serveur portent `Referrer-Policy: no-referrer`. L'adresse
  de l'instance ne part plus vers les sites tiers qu'une page contacte —
  l'aperçu de l'image chez le site importé, le lien du pied de page. Pour une
  installation auto-hébergée sur un domaine privé, c'est son existence qui
  cesse d'être annoncée. En contrepartie, l'aperçu d'une image distante peut
  ne plus s'afficher chez les sites qui refusent une requête sans `Referer`.
