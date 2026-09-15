- Un `robots.txt` démesuré ne met plus l'import en difficulté. Il est lu
  jusqu'à 512 Kio — la limite du récolteur de Google — et l'on n'en retient au
  plus que 500 règles ; ce qui dépasse l'une ou l'autre de ces bornes est
  ignoré, sans que le site soit refusé pour autant, et une directive tranchée
  en son milieu par la limite de taille est écartée plutôt qu'appliquée
  tronquée. Un import en lot ne peut donc plus épuiser la mémoire ni le
  processeur du serveur par des `robots.txt` volumineux, ni faire échouer en
  « délai dépassé » des pages parfaitement saines.
