- Un `robots.txt` démesuré ne met plus l'import en difficulté. Il est lu
  jusqu'à 512 Kio — la limite du récolteur de Google — et ce qui dépasse est
  ignoré, sans que le site soit refusé pour autant ; une directive tranchée en
  son milieu par cette limite est écartée plutôt qu'appliquée tronquée. Un
  import en lot ne peut donc plus épuiser la mémoire ni le processeur du
  serveur par des `robots.txt` volumineux, ni faire échouer en « délai
  dépassé » des pages parfaitement saines.
