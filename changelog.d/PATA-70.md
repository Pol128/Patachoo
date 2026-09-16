- Un `robots.txt` démesuré ne met plus l'import en difficulté, ni par le
  processeur ni par la mémoire. Il est lu jusqu'à 512 Kio — la limite du
  récolteur de Google — et ce qui dépasse est ignoré, sans que le site soit
  refusé pour autant ; une directive tranchée en son milieu par cette limite est
  écartée plutôt qu'appliquée tronquée. Chaque motif à joker n'est plus compilé
  qu'une fois, à la lecture du fichier, au lieu de l'être à chaque page jugée.
  Et de tout le fichier, une fournée ne garde que le groupe de règles qui nous
  vise — les groupes des autres robots n'étaient jamais relus —, dans la limite
  de cinq cents règles : un site qui en écrit davantage pour nous voit les
  suivantes ignorées, comme la queue d'un fichier au-delà du plafond. Un import
  en lot ne peut donc plus saturer le serveur par des `robots.txt` volumineux,
  ni échouer en « délai dépassé » sur des pages parfaitement saines.
