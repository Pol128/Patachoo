# Definition of Done

Ce qu'il faut avoir fait pour dire qu'une tâche est terminée. Chaque point se
vérifie par une commande ou par une question à réponse binaire — une DoD qu'on
ne peut pas cocher est une intention, pas une définition.

## 1. Ça compile et c'est propre

```sh
./verifie                # gofmt, go vet, go test, govulncheck — ~2 min
AVEC_RACE=1 ./verifie    # la même chose avec -race — ~13 min
```

Le script échoue au premier manquement. C'est le minimum, pas la DoD complète :
les points 2 à 5 ne s'automatisent pas.

**Une tâche livrée dépose ce qu'elle change dans `changelog.d/<son
identifiant>.md`**, dans la même demande de fusion que son code. Une ligne, en
français, qui dit ce que l'utilisateur verra de différent — pas le détail de
l'implémentation. Sans cette règle, le journal naît et meurt le même jour.

Un fichier par tâche, et non une ligne commune dans
[CHANGELOG.md](CHANGELOG.md) : deux tâches n'écrivent alors jamais au même
endroit, et ne peuvent plus se mettre en conflit sur leur seule entrée de
journal. `./journal` montre ce que « À paraître » contiendra ;
`./journal publier <version>` assemble le tout le jour venu.

**Deux passes, un seul script.** `./verifie` nu est la boucle courte, à lancer à
chaque geste. `AVEC_RACE=1 ./verifie` lance les mêmes tests sous le détecteur de
courses : c'est ce que la CI exécute à chaque poussée, et ce qu'il faut avoir
passé soi-même avant d'ouvrir une demande de fusion qui touche à du code
concurrent. Le produit est devenu concurrent — une goroutine par file d'hôte,
une cadence et un cache partagés —, et une course ne se voit dans aucune autre
commande du dépôt.

La passe longue dure six fois la courte : le détecteur multiplie la durée d'un
paquet qui monte des bases PocketBase, et `cmd/patachoo` en monte une par test.
Elle porte donc un `-timeout` explicite, largement au-dessus de cette durée — un
rouge doit parler d'une course, jamais d'un dépassement de délai.

Cette durée n'est pas une constante, et le plafond doit couvrir sa plage entière.
Relevé le 23/09/2026 sur une machine au repos, à 699 tests dans `cmd/patachoo` :
**~13 min**, dont autant pour le paquet `migrations`, désormais le plus long.
Elle était de 1 h 19, au repos aussi, avant que les tests de `cmd/patachoo` ne
recopient une base migrée une fois par passe au lieu d'en migrer une chacun
(PATA-129). Le runner est auto-hébergé sur la machine de développement, et
rien n'empêche un `./verifie` lancé à la main d'y croiser un job — ce qui a déjà
doublé la durée d'une passe. Le plafond est à une heure pour cette raison : il borne un test
bloqué, il ne borne pas une machine chargée.

## 2. Tests unitaires

La logique nouvelle est couverte, **cas d'échec compris**. Les cas d'échec sont
la moitié du travail : l'import échouera sur un site sur quatre, et c'est ce
comportement-là que l'utilisateur verra.

**Pas de seuil de couverture chiffré.** Un pourcentage se gonfle sans effort et
finit par mesurer le zèle plutôt que la qualité. La règle est celle-ci :

> Retirer le comportement doit faire rougir un test, et un seul.

Un test qui passe encore après qu'on a cassé ce qu'il prétend vérifier ne teste
rien. Un comportement qui en fait rougir douze indique des tests qui se répètent.
En cas de doute, casser volontairement la ligne concernée et relancer les tests :
c'est trente secondes, et ça répond.

## 3. Tests de sécurité

Là où le sujet existe — et il existe plus souvent qu'on ne croit.

- **SSRF.** L'import va chercher, *depuis le serveur*, une URL fournie par
  l'utilisateur. Un test par plage refusée — `localhost`, `127.0.0.0/8`,
  `169.254.0.0/16` (métadonnées cloud), `10.0.0.0/8`, `172.16.0.0/12`,
  `192.168.0.0/16` — **après résolution DNS**, sinon un nom de domaine qui
  pointe vers une IP privée passe au travers. Plus un test sur la redirection
  qui tente d'y revenir, et un sur les schémas autres que `http`/`https`.
  C'est le point le plus dangereux du produit.
- **Échappement.** Tout ce qui vient d'un utilisateur ou d'un site tiers et
  ressort dans une page a son test d'échappement. Une recette importée est du
  contenu étranger par nature.
- **Règles d'accès.** Les tests disent ce qu'un compte **ne peut pas** faire.
  Les recettes sont partagées entre comptes : la règle qui protège l'auteur se
  teste dans le sens du refus, pas seulement dans celui de l'autorisation.
- **Limites.** Taille de réponse, délai, nombre de redirections. Une valeur
  codée sans test finit augmentée « temporairement ».

## 4. Dépendances et licences

`govulncheck` passe — il est dans `./verifie`. Toute dépendance nouvelle
**redistribuée** est déclarée dans `NOTICE`, avec sa licence. Pas d'en-tête de
licence dans les fichiers source : `LICENSE` et `NOTICE` à la racine suffisent,
c'est tranché.

Et le garde-fou permanent : **aucune ligne reprise de Mealie ou Tandoor**
(AGPL-3.0).

## 5. Trace

La tâche Vikunja passe en Done avec un commentaire qui dit **ce qui a été
vérifié, et ce qui ne l'a pas été**. Un écart assumé et écrit vaut mieux qu'un
critère silencieusement contourné : c'est ce commentaire qui permet de relire
une décision six mois plus tard sans rouvrir le code.

---

## Ce qui a été tranché en écrivant ceci

**Pas de `gosec`.** `go vet` et `govulncheck` couvrent le réel — erreurs de
typage subtiles et CVE connues. `gosec` travaille par motifs et rendrait, sur
une base de cette taille, surtout des faux positifs qu'on apprendrait vite à
ignorer — c'est-à-dire le pire des deux mondes. À reconsidérer si le code de
manipulation de fichiers et d'URL grossit.

**Le point 1 tourne tout seul.** `.github/workflows/verifie.yml` lance
`./verifie` sur chaque poussée, sur chaque demande de fusion vers `main`, et une
fois par semaine sans que personne n'ait rien poussé : le seul point
automatisable de cette DoD ne dépend donc plus de la discipline de celui qui
pousse. Le workflow n'y réénumère aucun contrôle, il appelle le script ; un
critère ajouté à `./verifie` arrive en CI sans qu'on touche au YAML.

**Ce que la passe hebdomadaire ajoute**, et que les deux autres déclencheurs ne
peuvent pas donner : `govulncheck` interroge `vuln.go.dev` au moment où il
tourne. Déclenché par une poussée, son verdict a donc la date de la dernière
poussée, pas celle du jour — un projet posé, qui ne reçoit plus de commit
pendant des mois, est exactement celui dont les dépendances vieillissent sans
que rien ne le dise. Un rouge du lundi sur du code inchangé signale une faille
atteignable publiée depuis. Et sa limite, qui est une propriété et non une
objection : sur un dépôt public, GitHub désactive automatiquement les workflows
planifiés après **soixante jours sans activité sur le dépôt**, après un courriel
au propriétaire. La passe couvre l'intervalle de quelques semaines ; elle ne
couvre pas l'abandon.

**Les points 2 à 5 restent manuels**, et c'est la faiblesse connue de cette
DoD : aucun d'eux ne se lit dans un code de retour. Le point 5 est ce qui les
rend vérifiables — le commentaire Vikunja est la trace. Une CI verte dit que
rien n'est cassé, pas que la tâche est faite.

**Le rouge interdit la fusion.** `verifie` est un contrôle requis sur `main` :
une demande de fusion rouge ne peut pas être fusionnée. Ce fichier l'a longtemps
dit autrement — « à poser à la main » —, et c'est resté écrit après que le
réglage a été posé. Relevé le 16/09/2026, voici ce qu'il tient, et ce qu'il ne
tient pas :

- `verifie` requis, mais **non strict** : une branche n'a pas à être à jour sur
  `main` pour être fusionnée. Un vert obtenu avant plusieurs commits de `main`
  vaut donc encore, et c'est le trou connu de ce réglage — deux branches vertes
  séparément peuvent casser `main` ensemble.
- **Zéro approbation requise.** La boucle fusionne sans qu'un humain clique,
  ce qui est voulu : c'est la review automatique qui décide, et son refus se lit
  dans Vikunja plutôt que dans GitHub.
- **Résolution des fils exigée.** Un fil de review ouvert bloque la fusion, fût-il
  un détail. C'est ce qui tient une demande de fusion en « En revue » quand plus
  personne ne la regarde, et il faut le savoir pour ne pas chercher la cause du
  côté de la CI.
- Historique linéaire, ni force-push ni suppression de `main`.
- `enforce_admins` désactivé : le propriétaire passe outre. La protection défend
  de l'inattention, pas d'une décision.

Le réglage reste hors dépôt, sur une page de GitHub que ce fichier ne peut ni
garantir ni voir changer. C'est pourquoi il porte ici une date de relevé plutôt
qu'une affirmation au présent : ce qui est écrit là est vrai du jour où
quelqu'un est allé regarder.

**Rétroactivité.** PATA-1, PATA-2 et PATA-5 ont été livrées le 19/08/2026 sous
une version implicite de cette DoD : `gofmt`, `go vet`, tests unitaires, et un
test d'échappement HTML. `govulncheck` n'avait pas été passé — il l'a été depuis,
sans rien signaler. Rien à reprendre.
