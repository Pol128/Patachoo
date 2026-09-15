- Un `robots.txt` démesuré ne met plus l'import en difficulté. Il est lu
  jusqu'à 512 Kio — la limite du récolteur de Google — et ce qui dépasse est
  ignoré, sans que le site soit refusé pour autant ; une directive tranchée en
  son milieu par cette limite est écartée plutôt qu'appliquée tronquée. Chaque
  motif à joker n'est plus compilé qu'une fois, à la lecture du fichier, au
  lieu de l'être à chaque page jugée. Un import en lot ne peut donc plus
  saturer le processeur du serveur par des `robots.txt` volumineux, ni échouer
  en « délai dépassé » sur des pages parfaitement saines.
