- INSTALL.md décrit désormais l'installation **sans Docker**, en binaire : la
  ligne de construction complète avec son numéro de version, un tableau des
  machines qui dit quoi compiler pour un PC comme pour un Raspberry Pi — y
  compris les Pi 1 et Pi Zero, que l'image Docker ne couvre pas —, les deux
  drapeaux qu'un binaire lancé nu ne peut pas oublier (`--http`, sans quoi
  l'instance ne répond qu'à elle-même, et `--dir` en chemin absolu), la création
  du premier compte, la sauvegarde et la mise à jour. La section livre aussi une
  unité systemd complète et copiable telle quelle, qui relance le service après
  une panne et lui rend l'isolation que le conteneur donnait gratuitement. Elle
  dit franchement ce que le mode binaire fait gagner — rien sur une machine qui
  héberge déjà des conteneurs, environ 550 Mo sur une machine où Docker ne
  servait qu'à Patachoo — et qu'aucun binaire n'est publié : on compile soi-même
  depuis un tag.
