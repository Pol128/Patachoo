# Journal des versions

**Une section par version, la plus récente en haut.**

Ce qui n'est pas encore publié ne s'écrit plus ici : chaque tâche livrée dépose
son entrée dans `changelog.d/`, un fichier par tâche, et `./journal` les
assemble. Toutes les tâches écrivaient autrefois sur la même ligne de « À
paraître » — deux branches ouvertes en même temps entraient donc en conflit sans
rien avoir en commun, et la review dépensait un aller-retour de correction pour
une ligne de journal. `changelog.d/LISEZ-MOI.md` raconte la mesure qui a mené là.

    ./journal                 # ce que « À paraître » contiendra
    ./journal publier 0.2.0   # assemble les fragments ici, et les retire

Les numéros suivent [SemVer](https://semver.org/lang/fr/) et vivent dans le
dépôt sous forme de **tag git annoté, préfixé** — `v0.1.0`. Il n'y a pas de
fichier `VERSION` à la racine : il doublerait le tag, et les deux finiraient par
diverger.

## À paraître

_Rien pour l'instant._

## v0.3.0 — 2026-09-23

- La section « Installation par binaire » d'INSTALL.md ne dit plus qu'aucun
  binaire n'est publié : elle part désormais de l'archive toute compilée de la
  Release, et garde la compilation pour les Raspberry Pi 1 et Pi Zero premier
  modèle, qui n'ont pas d'archive, et pour qui préfère compiler. La mise à jour
  se fait en téléchargeant l'archive de la nouvelle version, en la vérifiant,
  puis en remplaçant l'exécutable et en redémarrant le service.
- La vérification automatique ne lance plus qu'une passe à la fois sur la
  machine qui l'héberge : les suivantes attendent leur tour au lieu de se
  partager les mêmes processeurs, et aucune n'est plus annulée pour faire de
  la place. Une demande de fusion peut donc attendre derrière une autre avant
  d'être vérifiée, mais chaque passe dure ce qu'elle doit durer.
- Pour qui contribue : la passe `AVEC_RACE=1 ./verifie` tient désormais en une
  douzaine de minutes, et la courte en deux. Les tests de `cmd/patachoo` ne
  migrent plus chacun leur base : ils recopient une base migrée une fois par
  passe, et chacun garde la sienne. Le `-timeout` de la passe longue redescend
  de trois heures à une.
- Un compte peut désormais être désigné comme curateur, en cochant une case sur
  sa fiche depuis l'interface d'administration. Le droit ne se donne pas
  autrement : personne ne peut se l'attribuer lui-même, et le retirer prend
  effet à la requête suivante, sans avoir à déconnecter qui que ce soit. Rien
  n'en est visible à cette étape — ce sont les écrans de l'établi, à venir, qui
  s'en serviront pour réserver leur accès.
- L'établi garde le verdict de l'œil humain, et le range en deux champs. Depuis
  la vue par aliment, un bouton ouvre le formulaire sur la ligne d'un groupe ;
  depuis la fiche d'une forme, le même formulaire porte sur cette seule ligne.
  Le verdict se pose par mots — saisie libre, liste existante proposée en
  premier, création à la volée —, dans un vocabulaire propre à l'établi qui ne
  se mélange pas aux tags des recettes. À côté, deux champs de texte, et leur
  séparation est le point de cet écran : ce qui pourra partir un jour vers
  patachoo.org parle de la règle ou de l'entrée du lexique, ce qui reste ici
  cite la ligne du corpus et n'en sort pas. Sur une ligne, la lecture attendue
  se saisit en plus champ à champ — « il fallait lire 2 oignons jaunes » —, et
  reste elle aussi du côté local. Chaque annotation garde l'empreinte de la
  passe qu'elle jugeait : elle dit quel parser elle visait, et se retrouve
  telle quelle après une nouvelle analyse du même corpus, la cible étant
  l'aliment canonique ou la ligne brute plutôt qu'une ligne d'analyse recréée à
  chaque passe. Un groupe portant au moins un mot de verdict quitte l'ordre par
  défaut de la vue agrégée ; une note sans mot l'y laisse. Et le verdict vaut
  tant que la lecture qu'il jugeait n'a pas changé : à la passe suivante, un
  groupe relu autrement revient dans la file avec son verdict précédent
  affiché, à confirmer d'un geste.
- L'établi descend du groupe à la ligne : `/etabli/formes` montre les formes
  distinctes d'une analyse — la ligne brute telle que le corpus la porte, ce
  que le parser en a lu champ à champ, ses signaux et le nombre de fois qu'elle
  a été vue —, et chaque forme a sa fiche. Une ligne par forme et non par
  occurrence : un verdict se pose une fois, même sur les milliers d'occurrences
  d'une ligne banale. La page s'ouvre par son adresse seule, se cherche par un
  champ qui ignore les accents et accepte les débuts de mot, et se parcourt
  page à page. Le chemin remonte aussi : depuis une forme, un lien mène au
  groupe qui l'a attrapée et dit combien d'autres formes s'y rangent ; depuis
  un groupe de la vue par aliment, une colonne descend sur ses formes. Quand
  l'analyse a porté sur la base de l'instance, la fiche d'une forme liste les
  recettes du carnet qui portent cette ligne, chacune menant à sa page ; un
  corpus fourni, lui, n'est pas conservé et n'a donc pas de provenance à
  montrer. Comme les autres écrans de l'établi, les deux pages sont réservées
  aux comptes qui en portent le droit, et ne modifient rien.
- L'établi montre le résultat d'une analyse regroupé par aliment :
  `/etabli/aliments`, réservé aux mêmes comptes que le lancement. Une ligne par
  aliment canonique — sa catégorie, ses signaux de relecture, le nombre de
  formes qu'il avale et le total de leurs occurrences — au lieu d'une ligne par
  forme : un verdict porté sur un groupe tranche toutes ses lignes d'un coup.
  L'écran s'ouvre sur ce qui coûte le plus cher à relire, c'est-à-dire les
  groupes qui portent au moins un signal et que personne n'a encore tranchés,
  classés par occurrences décroissantes ; la liste complète reste à un clic.
  Trois autres ordres de lecture sont proposés : par dispersion — combien de
  formes brutes une entrée avale —, par catégorie, et par rareté. Les aliments
  que le lexique ne connaît pas forment leur propre tas, sans catégorie : c'est
  sa file d'attente. La vue porte sur la dernière analyse terminée, et un
  paramètre d'adresse permet d'en relire une autre.
- L'établi a sa page de lancement, réservée aux comptes qui portent le droit de
  curateur : `/etabli`. Deux entrées, et une seule à la fois — les lignes
  d'ingrédients de l'instance, lues et jamais modifiées, ou un corpus fourni,
  collé dans le champ ou joint en fichier, une ligne d'ingrédient par ligne. Le
  corpus fourni n'est conservé nulle part : il est lu en mémoire, analysé, et
  seules les formes qu'il donne sont écrites. La page annonce la taille acceptée
  avant l'envoi et refuse ce qui la dépasse en le disant, montre l'avancement de
  la passe en cours et le résultat de la dernière. Les lancements sont plafonnés
  à cinq par minute et par adresse.
- Patachoo sait désormais passer un corpus entier de lignes d'ingrédients au
  parser, hors de toute page : les lignes de la base de l'instance, ou un
  fichier du disque de la machine. Le travail dédoublonne les lignes en formes
  distinctes, compte leurs occurrences, garde la lecture du parser et les
  signaux de relecture de chacune. Rien n'est écrit dans les recettes ni dans
  leurs ingrédients : l'établi lit le carnet, il ne le modifie pas. Une analyse
  à la fois, et trois garde-fous qui refusent un corpus hors de proportion —
  500 000 lignes, 32 Mio, trente minutes. Deux commandes le pilotent en
  attendant ses écrans : `patachoo analyse lancer [fichier]` et
  `patachoo analyse resume <identifiant>`, qui rend le compte des formes, des
  signaux, des aliments reconnus ou non, et leur répartition par catégorie.
- La base sait désormais accueillir ce que produira l'établi d'analyse de
  corpus : une passe d'analyse, les formes distinctes qu'elle a lues avec leur
  nombre d'occurrences et leur provenance, et les annotations portées dessus.
  Les formes se cherchent sans accents ni majuscules — « creme » ramène
  « Crème ». Rien n'en est visible à cette étape : ce sont les écrans de
  l'établi, à venir, qui rempliront et liront ces tables, et l'API reste
  réservée à l'administration de l'instance.
- Patachoo sait désormais dire d'une ligne d'ingrédient ce qui a pu mal se
  passer à sa lecture : un mot du texte d'origine que plus aucun champ ne
  porte, un aliment resté vide, une unité répétée dans l'aliment — « 1 feuille
  de feuille de laurier » —, un aliment absent du dictionnaire, ou des mots
  remis dans un autre ordre. Rien n'en est visible à cette étape : ce sont les
  écrans de relecture, à venir, qui s'en serviront pour trier ce qui mérite un
  œil.
- `patachoo analyse comparer` dit ce que la montée du parser change sur un
  corpus, sans avoir besoin d'une vérité de référence : entre deux analyses du
  même corpus, ou entre les colonnes déjà écrites dans les ingrédients et une
  analyse, ce qui est la seule façon de comparer à une version du parser qui ne
  se rejoue plus. Le rapprochement se fait sur la ligne brute, qui ne bouge
  jamais. La sortie donne de part et d'autre le nombre de formes qu'aucun signal
  de relecture n'allume, le taux d'aliments reconnus, le compte de chaque signal
  et de chaque motif de lecture, avec leur écart ; puis les formes qui ont
  changé, rangées par famille — celles dont seul l'aliment prend sa forme
  canonique, tenues hors du verdict, celles qui n'allumaient aucun signal et en
  allument désormais, listées en entier pour être relues, et le reste. Rien
  n'est écrit dans les recettes ni dans leurs ingrédients. `analyse resume`
  gagne au passage le compte des formes sans aucun signal.
- La section d'`INSTALL.md` qui explique comment vérifier la provenance de
  l'image dit maintenant quelle version de `gh` la commande réclame — 2.49 au
  minimum —, comment lire la sienne, et ce que rend un `gh` plus ancien : un
  `unknown command`, qui ressemble à une coquille dans la page sans en être
  une. Pour qui ne peut pas mettre `gh` à jour, elle donne une voie de repli
  avec `curl` et `jq` seuls, qui lit l'attestation directement dans le
  registre et montre ce qu'elle affirme. Cette voie dit aussi, sans détour, ce
  qu'elle ne fait pas : elle ne vérifie aucune signature.
- Patachoo s'installe désormais **en une commande** sur un Linux sans Docker ni
  Go : `installer.sh`, publié avec chaque Release, détecte la plateforme,
  télécharge l'archive, **compare sa somme** et pose le binaire — dans
  `/usr/local/bin` ou le préfixe choisi, pour la dernière version ou celle
  demandée. Il est lui-même couvert par les sommes et l'attestation de
  provenance : INSTALL.md montre d'abord comment le vérifier et le lire avant
  de le lancer. Il ne crée ni compte ni service, et renvoie macOS, Windows et
  les Raspberry Pi en armv6 vers la compilation depuis les sources.
- Chaque version publie désormais Patachoo **tout compilé**, dans sa Release
  GitHub : une archive par plateforme de l'image (`linux_amd64`, `linux_arm64`,
  `linux_armv7`), sous des noms qui ne changeront plus d'une version à l'autre,
  avec un fichier de sommes de contrôle et une attestation de provenance. Il
  n'est plus nécessaire d'installer Go pour se passer de Docker. INSTALL.md
  dit où les télécharger, comment vérifier les sommes et la provenance, et ce
  que cette vérification prouve — et ne prouve pas.
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
  servait qu'à Patachoo.
- Le rythme d'au plus une requête par seconde vaut désormais par site, et non
  par nom d'hôte : les sous-domaines d'un même site — `a.exemple.fr`,
  `b.exemple.fr` — se partagent ce rythme, et le délai entre deux visites
  qu'en demande l'un vaut pour tous. Dans un import en lot, ils se suivent au
  lieu de partir ensemble, sans retenir les autres sites de la fournée. Deux
  sites hébergés sous le même nom de service — deux blogs sur un même
  hébergeur, par exemple — comptent désormais comme un seul, et une fournée qui
  les mêle est plus lente.
- Les requêtes que Patachoo envoie aux sites visités suivent maintenant toutes
  le même rythme : au plus une par seconde et par site, que l'adresse vienne
  d'un import en lot, d'un import à l'unité ou du téléchargement d'une
  illustration. Un import lancé à la main ne fait plus tourner l'écran plus de
  cinq secondes pour attendre son tour : au-delà il renonce et le dit — que le
  site soit déjà parcouru par un lot, ou qu'il demande lui-même de longs délais
  entre deux visites. Un import en lot, lui, patiente le temps qu'il faut.
- L'import d'une adresse est limité à dix par minute et par adresse IP.
  Au-delà, la page revient avec son message et l'adresse saisie.

## v0.2.0 — 2026-09-19

- La page « Importer un lot » liste désormais vos vingt dernières fournées, avec leur date, leur tag et leur état : le rapport d'un import se retrouve après coup, et plus seulement au moment où il s'affiche.
- Dans le rapport d'une fournée, chaque adresse qui n'a pas abouti porte une case à cocher. Elle reste cochée quand vous revenez sur la page, et l'en-tête dit combien d'adresses vous avez reprises sur le total des échecs.
- Sur la fiche d'une recette, l'aliment s'accorde désormais à sa quantité : « 3 tomates » là où la ligne affichait « 3 tomate » depuis que l'aliment est enregistré sous sa forme du dictionnaire. Une quantité de un, une quantité à virgule — « 1,5 » — ou une quantité absente laissent le singulier, et un aliment que le dictionnaire ne connaît pas s'affiche tel quel, sans pluriel inventé.
- Les ingrédients des recettes importées sont désormais mieux lus : la préparation qui suit une virgule — « 2 oignons, hachés finement » — part en note au lieu de rester collée à l'aliment, la quantité écrite après l'aliment — « Aubergines : 500 g » — n'est plus perdue, la contenance d'un contenant — « 1 boîte de 796 ml de tomates broyées » — sort de l'aliment, et « 250 gramme(s) + 200 gramme(s) » compte bien 450 g. Les recettes déjà en base ne sont pas reprises : seuls les imports suivants en profitent.
- `INSTALL.md` explique enfin comment ouvrir un compte à quelqu'un, et pas
  seulement comment créer le superutilisateur : les deux chemins — créer le
  compte depuis `/_/`, ou ouvrir l'inscription le temps qu'il s'inscrive puis la
  refermer —, ce que ce compte donne, et ce qu'il faut savoir avant d'ouvrir
  (mot de passe d'au moins 8 caractères, pas de vérification de courriel,
  dix inscriptions par heure et par adresse IP).
- Le README renvoie vers le guide d'installation dès sa section « Démarrer » : installer une instance — image Docker, adresse et port, TLS, sauvegarde et restauration — ne se confond plus avec faire tourner le dépôt depuis les sources.

## v0.1.0 — 2026-09-17

- Chaque version publiée a désormais sa page sur GitHub. Le numéro affiché dans
  le pied de page renvoyait vers une liste de versions que rien ne remplissait —
  un tag seul n'y apparaît pas. La CI y dépose maintenant, pour chaque version,
  la section du journal qui la décrit et l'empreinte exacte de l'image publiée,
  avec la commande qui en vérifie la provenance. Une pré-version y est marquée
  comme telle, et ne se présente pas comme la dernière version disponible.
- L'installation et l'utilisation courante du carnet sont désormais vérifiées de
  bout en bout sur le binaire livrable, lancé comme on le lance chez soi :
  l'instance neuve porte close, le superutilisateur qui ouvre l'inscription, le
  premier compte créé et connecté — puis une recette écrite au formulaire,
  retrouvée sur la liste et sur sa fiche, corrigée, et la déconnexion qui referme
  derrière elle. Rien ne change pour qui s'en sert ; ce qui change, c'est qu'une
  version qui casserait l'un de ces deux parcours ne peut plus sortir verte.
- Les réponses du serveur portent `Referrer-Policy: no-referrer`. L'adresse
  de l'instance ne part plus vers les sites tiers qu'une page contacte —
  l'aperçu de l'image chez le site importé, le lien du pied de page. Pour une
  installation auto-hébergée sur un domaine privé, c'est son existence qui
  cesse d'être annoncée. En contrepartie, l'aperçu d'une image distante peut
  ne plus s'afficher chez les sites qui refusent une requête sans `Referer`.
- Les pages ne sont plus conservées par les caches. Le serveur répond
  `Cache-Control: private, no-store` sur ses pages et ses fragments : un proxy
  ou un CDN placé devant l'instance ne peut plus servir à un visiteur la page
  rendue pour le compte d'un autre. Le navigateur redemande donc la page au
  serveur lors d'une navigation arrière. La feuille de style, htmx et les
  illustrations restent mis en cache comme avant.
- Les pages portent désormais une politique de sécurité du contenu stricte.
  Un script qui se glisserait dans une recette importée, un commentaire ou un
  nom de compte n'a plus de quoi s'exécuter : le navigateur le refuse, et le
  carnet reste hors d'atteinte. Rien ne change à l'usage — l'aperçu de l'image
  d'un site importé s'affiche toujours, et le panneau d'administration garde le
  sien.
- L'image Docker est construite et publiée par la forge, sur tag de version, et
  porte une attestation de provenance qui la relie au commit dont elle sort.
  Chaque version publie ses références `0.1.0` et `0.1` à côté de `latest` ;
  `INSTALL.md` dit comment épingler l'image par empreinte pour décider soi-même
  quand on monte de version, et comment vérifier d'une commande que celle qu'on
  a tirée vient bien de ce dépôt.
- L'installation ne conseille plus d'écrire le mot de passe d'administration sur
  la ligne de commande. `INSTALL.md` met en premier l'URL affichée dans les logs
  au premier démarrage, qui crée le compte depuis le navigateur ; les commandes
  `superuser upsert` qui restent, là et dans le `README`, disent ce qu'un
  argument laisse voir dans `ps` et dans l'historique du shell, et comment s'en
  prémunir ; les blocs `curl` de sauvegarde, de restauration et de répétition
  lisent désormais le mot de passe au clavier.
- La documentation dit comment exposer Patachoo hors de chez soi : une section
  d'`INSTALL.md` donne le proxy inverse en HTTPS, la variante du compose qui
  l'accompagne, et la façon de fermer `/_/` de l'extérieur. Le tableau des
  chemins et le `README` avertissent désormais que `/_/` et `/api/` répondent
  sur le même port que le carnet et ne doivent pas être publiés sans filtre
  devant. Le chiffrement des réglages est aussi documenté pour Docker.
- Les deux workflows de la forge n'exécutent plus que des actions épinglées par
  empreinte de commit, la version exacte gardée en commentaire sur la même
  ligne. Ce que la CI exécute est donc décidé par le dépôt : un tag comme `v4`
  est redéployé par son mainteneur à chaque version mineure, et changeait sous
  ce nom sans qu'une ligne bouge ici — sur la vérification comme sur la
  publication de l'image, celle qui signe une attestation de provenance. Aucune
  action ne change de lignée majeure au passage : on fige ce qui tournait déjà.
  L'en-tête de `.github/workflows/verifie.yml` porte la convention et la
  commande qui remonte une empreinte le jour où l'on veut monter de version.
- `./verifie` ne télécharge plus la dernière version publiée de `govulncheck` au
  moment où il tourne : il en exécute une version écrite en toutes lettres dans
  le script, `v1.8.0`. Ce que la vérification exécute est donc décidé par le
  dépôt et lisible sans rien lancer, là où `@latest` laissait le proxy de
  modules choisir — sur la machine de développement comme sur le runner de CI,
  à chaque poussée. L'analyse ne perd rien en fraîcheur : la base de
  vulnérabilités est interrogée sur `vuln.go.dev` à l'exécution, indépendamment
  de la version de l'outil. Le module reste hors de `go.mod` ; le script dit en
  commentaire comment remonter ce numéro le jour venu.
- `INSTALL.md` explique le réglage à faire derrière un proxy inverse. Sans lui,
  le plafond de cinq tentatives de connexion par minute compte tous les
  visiteurs sur un seul compteur, et « trop de tentatives » finit par être
  opposé à des gens qui n'ont rien tenté. La section dit quel en-tête déclarer
  dans `/_/`, laquelle des adresses est retenue, pourquoi ne jamais le déclarer
  sans proxy, et comment vérifier le résultat.
- La connexion et l'inscription ne lisent plus leurs champs que dans le corps
  du formulaire. Un mot de passe placé dans l'adresse — `?courriel=…&mot-de-passe=…` —
  n'ouvre plus de session et ne crée plus de compte : il cessait d'être un
  secret dès la première requête, l'adresse complète étant recopiée dans les
  journaux du serveur, dans les sauvegardes de la nuit et dans l'historique du
  navigateur.
- `INSTALL.md` explique comment servir Patachoo en HTTPS hors de la machine
  locale — proxy inverse, certificat obtenu par Patachoo lui-même, ou simple
  tunnel SSH — et nomme le symptôme d'un accès en clair depuis un autre poste :
  la connexion est acceptée mais reste sans effet, parce que le navigateur jette
  un cookie `Secure` venu d'une origine qui ne l'est pas. Rien ne change dans le
  produit ; c'est la documentation qui décrivait un déploiement impossible.
- Le cookie de session n'ouvre plus que les pages du produit. Il
  n'authentifie plus les adresses `/api/` ni `/_/` : une faille d'affichage
  dans une page n'y gagne donc plus l'API REST du compte connecté, avec ses
  collections entières et ses opérations de compte. Rien ne change à l'usage —
  les pages, les formulaires et les vignettes des recettes se comportent comme
  avant — et un client d'API qui porte son propre jeton reste servi.
- La déconnexion ne répond plus qu'à un compte connecté. Une demande qui
  n'accompagne aucune session ouverte est renvoyée à la page de connexion sans
  rien effacer : seul le navigateur qui tient la session peut y mettre fin.
- Un site tiers ne peut plus vous connecter sur un compte qu'il contrôle. Les
  formulaires de Patachoo portent désormais un jeton que le serveur vérifie à
  la soumission : une page extérieure qui posterait toute seule sur la
  connexion, l'inscription ou n'importe quelle autre action est refusée avant
  d'avoir rien changé. Un formulaire laissé ouvert très longtemps peut à son
  tour être refusé : la page invite alors à recharger et à recommencer.
- Le cookie de session change de nom et **ce déploiement déconnecte tout le
  monde une fois** : l'ancien cookie n'est plus lu, il faut se reconnecter. Le
  nouveau nom porte le préfixe `__Host-`, qui fait refuser par le navigateur
  tout cookie de session qu'un sous-domaine voisin — un `blog.exemple.fr` à
  côté d'un `patachoo.exemple.fr` — tenterait de poser à sa place. Rien à
  changer à l'installation.
- Une session a désormais une durée de vie maximale de trente jours. Passé ce
  délai, elle cesse de se prolonger toute seule et s'éteint dans les cinq jours
  qui suivent : il faut alors se reconnecter. Les sessions déjà ouvertes au
  moment de la mise à jour n'ont pas la date d'ouverture que ce plafond
  réclame ; elles s'arrêtent de se prolonger tout de suite et demandent une
  nouvelle connexion sous cinq jours.
- « Se déconnecter » referme désormais la session **sur tous les appareils** du
  compte, et plus seulement dans le navigateur d'où le geste est fait. Les
  accès déjà ouverts ailleurs — un autre navigateur, un téléphone, un poste
  partagé qu'on a quitté sans fermer — cessent aussitôt de fonctionner et
  demandent une nouvelle connexion. Rien à changer à l'installation.
- Une image téléversée est mesurée avant d'être rangée. Ses dimensions
  passent désormais le même garde-fou de quarante mégapixels qu'une image
  téléchargée depuis un site : au-delà, le formulaire revient avec un message
  sur le champ image et rien n'est enregistré. Un fichier léger mais démesuré —
  un aplat de 30 000 × 30 000 tient dans quelques centaines de kibioctets —
  faisait jusqu'ici allouer des gibioctets au serveur à la première vignette
  demandée, et la page d'accueil recommençait à chaque visite. Les formats que
  le formulaire accepte ne changent pas, AVIF compris.
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
- Une grosse fournée d'import ne laisse plus s'accumuler des connexions
  sortantes inactives. Chaque récupération de page fermait les siennes au bout
  de quatre-vingt-dix secondes seulement : un lot de cinq cents adresses pouvait
  ainsi épuiser les descripteurs de fichiers du serveur, qui cessait alors de
  répondre à tout le monde, sans rien écrire dans les journaux. Elles sont
  désormais fermées dès la page récupérée.
- L'import refuse les adresses privées écrites en IPv6. Une URL qui enferme
  une IPv4 interne dans une écriture NAT64 (`64:ff9b::/96`), 6to4
  (`2002::/16`) ou compatible v4 (`::/96`) — par exemple
  `http://[64:ff9b::a9fe:a9fe]/` pour 169.254.169.254 — est désormais refusée
  comme l'est déjà sa forme v4. Les adresses publiques écrites sous ces mêmes
  formes restent importables.
- L'import refuse désormais les adresses d'un tailnet ou d'un réseau CGNAT, au
  même titre que les adresses privées. Une installation posée sur un tailnet —
  Tailscale numérote ses pairs dans la plage partagée `100.64.0.0/10` — ne peut
  plus servir à en explorer les machines depuis une URL collée dans l'import.
  Trois plages réservées voisines sont refusées avec elle : `0.0.0.0/8`,
  `198.18.0.0/15` et `240.0.0.0/4`.
- L'authentification par l'API est plafonnée comme la page de connexion : cinq
  tentatives par minute et par adresse sur
  `POST /api/collections/users/auth-with-password`, là où la règle livrée par
  PocketBase en laissait quarante. La porte d'à côté gardait son plafond, celle-ci
  ne l'avait pas. L'authentification d'administration, elle, ne bouge pas.
- Une recette ne se modifie plus que par le compte qui l'a ajoutée, dans le
  carnet comme par l'API : ses champs et ses ingrédients. Tout compte connecté
  pouvait jusqu'ici vider le titre, les instructions et la source d'une recette,
  puis en retirer les lignes une à une — il restait une fiche blanche que son
  auteur seul pouvait effacer, sans rien à récupérer. Le lien « Modifier »
  n'apparaît donc plus que sur ses propres recettes. Le carnet reste partagé
  pour tout le reste : on lit, on cherche et on commente les recettes de tout le
  monde, et corriger la coquille d'un autre passe désormais par une note.
- Une note reste signée du compte qui l'a écrite, et rattachée à la recette sous
  laquelle elle a été écrite. L'API acceptait qu'un compte retourne sa propre
  note au nom d'un autre, ou la déplace sous une autre recette : les deux champs
  sont désormais figés à la modification. Le carnet et ses pages ne changent pas.
- Le dépôt dit où signaler une faille. `SECURITY.md` donne le canal — le
  signalement privé de GitHub, et lui seul —, ce qu'un bon signalement contient,
  et ce qu'il faut attendre d'une réponse sur un projet tenu sur temps libre.
  Le `README` y renvoie.
- Les données ne sont plus lisibles par les autres comptes de la machine. Le
  serveur crée `pb_data`, ses bases et ses sauvegardes en `0700` et `0600` :
  `data.db` porte en clair de quoi fabriquer un jeton d'administration, et
  n'importe quel compte local pouvait le lire. Une instance déjà installée garde
  les droits de ses fichiers existants — `INSTALL.md` donne la commande de
  rattrapage.
- Le lancement d'un import en lot est plafonné : cinq fournées par minute et par
  adresse. Au-delà, la page de saisie revient avec un message et la liste collée
  encore dans le champ, au lieu du JSON brut d'une erreur d'API. Et une minute
  dont tous les noms de fournée sont déjà pris n'est plus une panne : elle rend
  la même page et invite à réessayer, là où elle rendait une erreur.
- La création de comptes est plafonnée. Une même adresse dispose de dix
  inscriptions par heure ; au-delà, la page d'inscription revient sous un
  « Trop de tentatives d'inscription », sans qu'aucun compte de plus soit créé.
  Une instance qui garde son inscription fermée continue de répondre comme
  avant. Le plafond de la page de connexion, lui, ne bouge pas.
- La source d'une recette se saisit et se corrige. Le formulaire porte un champ
  « Source — adresse de la page d'origine », prérempli par l'import et visible à
  la création comme à l'édition : une recette tapée à la main ou importée à
  l'unité affiche désormais son origine, là où seules les fournées le
  faisaient. Changer l'adresse oublie le nom du site enregistré, qui désignait
  l'ancienne ; vider le champ retire la source.
- Le binaire porte sa version. `patachoo version` et `patachoo --version`
  l'impriment, et le pied de page l'affiche à un compte connecté, en lien vers
  les versions publiées. Construit sans `-ldflags`, il annonce ce qu'il est —
  `dev (1de2cd1, modifié)` — plutôt que de se taire.
- Les recettes importées en fournée arrivent illustrées. L'image que la page
  publie est téléchargée et attachée comme à l'import unitaire ; une image
  injoignable ou refusée ne coûte plus la recette, elle la laisse simplement
  sans illustration. Les fournées d'avant ne sont pas rattrapées.
- Les sources du programme vivent dans `cmd/patachoo/`, gabarits et assets
  compris : la racine du dépôt ne porte plus que ses fichiers d'accueil. Rien
  ne change pour qui installe le binaire ou l'image ; qui construit depuis les
  sources écrit désormais `go run ./cmd/patachoo serve` et
  `go build -o Patachoo ./cmd/patachoo`.
