# Installation

Patachoo tient dans un binaire unique : pas de base de données à installer à
côté, pas de serveur d'applications à configurer devant. Toutes ses données
vivent dans un seul répertoire, `pb_data`.

## Installation par Docker

L'image couvre `linux/amd64`, `linux/arm64` et `linux/arm/v7` sous un même
manifeste : `docker pull` ramène la bonne architecture sans qu'on ait à la
nommer — un x86, un NAS ARM et un Raspberry Pi tirent la même référence.

Elle est construite `FROM scratch` : elle ne contient que le binaire, le magasin
de certificats, `/tmp` et `/pb_data`. Il n'y a donc ni shell ni gestionnaire de
paquets dedans, et c'est voulu — presque aucune surface de vulnérabilité à
suivre, rien à mettre à jour entre deux versions de Patachoo. La contrepartie :
`docker exec … sh` ne fonctionnera pas, il n'y a pas de `sh`.

### Démarrer

Déposer ce `docker-compose.yml` dans un répertoire vide — c'est le même fichier
qu'à la racine du dépôt :

```yaml
services:
  patachoo:
    image: ghcr.io/pol128/patachoo:latest
    restart: unless-stopped
    ports:
      - "8090:8090"
    volumes:
      - pb_data:/pb_data

volumes:
  pb_data:
```

puis, dans ce répertoire :

```sh
docker compose up -d
```

Sans `compose`, la commande complète équivalente :

```sh
docker run -d --name patachoo --restart unless-stopped \
    -p 8090:8090 -v patachoo_pb_data:/pb_data \
    ghcr.io/pol128/patachoo:latest
```

### L'adresse et le port

L'application répond sur le **port 8090** de la machine hôte, soit
`http://<adresse-de-la-machine>:8090` — et `http://localhost:8090` si c'est la
machine devant laquelle on est assis :

| Chemin        | Ce qu'on y trouve                  |
|---------------|------------------------------------|
| `/`           | le carnet de recettes              |
| `/_/`         | l'interface d'administration       |
| `/api/`       | l'API REST                         |
| `/api/health` | la sonde de santé                  |

Dans le conteneur, Patachoo écoute sur `0.0.0.0:8090`, et cela ne change pas :
si 8090 est déjà pris sur la machine, c'est le **port de gauche** de
`"8090:8090"` que l'on modifie, par exemple `"8091:8090"`.

### Créer le premier superutilisateur

Rien n'est créé à l'avance : il faut un premier compte d'administration.

**Par le navigateur — le chemin conseillé.** Au premier démarrage, tant
qu'aucun superutilisateur n'existe, les logs affichent une URL qui mène au
formulaire de création :

```sh
docker compose logs patachoo
```

La ligne à suivre ressemble à
`http://0.0.0.0:8090/_/#/pbinstall/<un-long-jeton>` ; remplacez l'hôte et le
port par ceux d'où vous ouvrez le navigateur. Le jeton vaut **trente minutes** —
passé ce délai, redémarrer le conteneur en affiche un neuf tant que le compte
n'existe pas. Le mot de passe se tape alors dans un formulaire : il ne passe ni
par la table des processus, ni par l'historique du shell.

**En repli, par la ligne de commande.**

```sh
docker compose run --rm patachoo superuser upsert vous@exemple.fr 'un-mot-de-passe-solide'
```

Sur un conteneur déjà lancé, sans `compose` :

```sh
docker exec patachoo /patachoo superuser upsert vous@exemple.fr 'un-mot-de-passe-solide'
```

> **Ces deux commandes portent le mot de passe en argument, et un argument se
> lit deux fois.** Le temps de la commande, `ps aux` montre la ligne complète à
> **tout utilisateur de la machine** — sur un NAS, le compte de sauvegarde ou
> celui du média. Et la commande reste **en clair dans l'historique du shell**
> (`~/.bash_history`, `~/.zsh_history`), un fichier que rien ne protège, qui
> part dans la sauvegarde du poste et suit les dotfiles qu'on synchronise : des
> mois plus tard, le mot de passe du compte qui peut tout y est encore lisible.
>
> Deux remèdes, à défaut de mieux : préfixer la commande d'**une espace** quand
> `HISTCONTROL` contient `ignorespace`, ou effacer la ligne juste après avec
> `history -d <numéro>`, le numéro venant de `history`. La variante
> `docker exec` laisse en plus la commande dans l'enregistrement de l'exec, que
> `docker inspect` rend — le `--rm` de `docker compose run` efface le sien,
> celui-là reste.

Ce compte donne accès à `/_/`. Il administre l'instance ; ce n'est pas le compte
avec lequel on range ses recettes au quotidien.

### Ce qu'il faut sauvegarder

**`pb_data`, et rien d'autre.** La base SQLite, les fichiers téléversés et les
sauvegardes y sont tous. Le reste — l'image, le `docker-compose.yml` — se
retrouve en une commande.

Avec le `docker-compose.yml` ci-dessus, c'est un volume Docker nommé
`<nom-du-répertoire>_pb_data` ; `docker volume inspect` en donne le chemin réel
sur l'hôte.

Ce chemin sert à *retrouver* les données, pas à les copier : voir « Sauvegarde
et restauration » plus bas, une copie de fichiers prise à chaud donne une base
corrompue.

### Droits sur le volume, et bind mount

Le conteneur **ne tourne pas en `root`** : son processus a l'UID **65532** (le
`nonroot` des images distroless), et `/pb_data` lui appartient dans l'image.

Un **volume nommé** encore vide hérite de ce propriétaire. Il n'y a donc rien à
faire — c'est le cas du `docker-compose.yml` ci-dessus, et la raison pour
laquelle il marche tel quel.

Un **bind mount** — `-v /volume1/docker/patachoo:/pb_data` — garde en revanche le
propriétaire du répertoire de l'hôte. C'est la première cause d'échec au
démarrage sur NAS : le conteneur redémarre en boucle parce qu'il ne peut pas
écrire dans son propre répertoire de données. Le régler **avant** le premier
`up` :

```sh
sudo mkdir -p /volume1/docker/patachoo
sudo chown -R 65532:65532 /volume1/docker/patachoo
```

(en remplaçant `/volume1/docker/patachoo` par le chemin choisi.)

#### `pb_data` n'est lisible que par le compte du serveur

Le serveur crée son répertoire de données, ses bases et ses sauvegardes **sans
accorder le moindre droit aux autres comptes de la machine** : `0700` pour les
répertoires, `0600` pour les fichiers. Ce n'est pas un réglage, et il n'y a rien
à faire pour l'obtenir.

Ce n'est pas de la prudence de principe. `data.db` porte **en clair** les
secrets de signature des jetons d'authentification : qui peut lire ce fichier
peut fabriquer un jeton d'administration valable et entrer dans `/_/` sans
connaître aucun mot de passe. Une archive de `pb_data/backups/` contient le même
fichier. Sur une machine que le serveur partage avec d'autres services — un
binaire sous systemd, un NAS, un conteneur voisin monté sur le même chemin —
c'est la différence entre un compte local quelconque et l'administration
complète de l'instance.

**Sur une instance déjà installée**, les fichiers écrits avant cette version
gardent leurs droits : un umask ne vaut que pour ce qui est créé après. Serveur
arrêté, rattrapage en une commande :

```sh
chmod -R go-rwx /volume1/docker/patachoo
```

(sur le chemin de votre `pb_data` ; pour un volume Docker nommé, `docker volume
inspect` en donne le chemin réel sur l'hôte.) La vérifier avec
`ls -ln /volume1/docker/patachoo` : plus aucun droit ne doit figurer dans les
deux derniers groupes de trois caractères.

### Mettre à jour

```sh
docker compose pull
docker compose up -d
```

Seule l'image change ; les données restent dans le volume. Sauvegarder `pb_data`
avant une montée de version reste la précaution d'usage.

### Fuseau horaire

Les tâches planifiées de Patachoo tournent en **UTC**, quel que soit le fuseau
de la machine hôte. L'image ne contient pas de base de fuseaux horaires et ne lit
pas la variable `TZ` : une tâche programmée à 3 h partira à 3 h UTC. C'est à
prendre en compte au moment de choisir l'heure, pas à corriger dans l'image.

### Santé du conteneur

L'image porte son propre `HEALTHCHECK` : `docker compose ps` et `docker ps`
affichent `healthy` quand l'application répond. Il n'y a rien à ajouter dans le
`docker-compose.yml` — le répéter en donnerait deux à maintenir.

La sonde est une sous-commande du binaire lui-même, `patachoo healthcheck`, qui
interroge `/api/health`. C'est ce que le `FROM scratch` impose : sans shell, il
n'y a ni `curl` ni `wget` à appeler.

## Sauvegarde et restauration

`pb_data/` contient **toute** la base et **toutes** les images. C'est le seul
répertoire à sauvegarder, et le seul dont la perte est irréparable : le binaire
se recompile, le schéma vit dans le dépôt, les recettes non.

> **Ne jamais sauvegarder `pb_data/` par simple copie de fichiers.** Une base
> SQLite copiée pendant qu'elle écrit donne une archive corrompue — pas
> systématiquement, ce qui est bien pire : on ne s'en aperçoit qu'au moment de
> restaurer. `rsync`, `cp -r`, un instantané de volume pris à chaud : tous
> exposent au même risque.

Patachoo utilise la sauvegarde intégrée de PocketBase, qui pose un point de
contrôle sur le journal d'écriture (`PRAGMA wal_checkpoint(TRUNCATE)`) et
archive `pb_data` dans une transaction. C'est une sauvegarde à chaud correcte,
serveur en marche.

### Ce qui est réglé par défaut

Une migration du dépôt pose, sur base neuve comme sur base existante :

| Réglage | Valeur | Ce que ça veut dire |
|---|---|---|
| `Backups.Cron` | `0 3 * * *` | une archive chaque nuit à 3 h, heure du serveur |
| `Backups.CronMaxKeep` | `3` | les trois dernières archives automatiques sont gardées |

PocketBase, lui, laisse le cron **vide** par défaut : sans cette migration, une
installation fraîche ne sauvegarderait rien.

Le réglage est posé une seule fois. Si vous le changez dans `/_/`, un
redémarrage ne le réécrasera pas.

### Où atterrissent les archives

Dans `pb_data/backups/`, sous forme de fichiers `.zip`. Chacun contient
`data.db`, `auxiliary.db` et le répertoire `storage/` — c'est-à-dire les images
des recettes. Sont exclus du fichier : `backups/` lui-même et les répertoires
temporaires.

Une archive pèse à peu près le poids de `pb_data`, et sa fabrication demande
**deux fois** cette taille en espace libre le temps de l'écrire.

Les archives automatiques sont nommées `@auto_pb_backup_<horodatage>.zip` ; ce
préfixe n'est pas décoratif, voir « Trois comportements qui surprennent ».

### Changer la fréquence et le nombre d'archives gardées

Dans `/_/` → **Settings** → **Backup and restore** :

- **Enable auto backups** : l'interrupteur. Le décocher coupe la sauvegarde
  automatique — c'est-à-dire vide `Backups.Cron`.
- **Cron expression** : cinq champs. `0 3 * * *` pour chaque nuit à 3 h,
  `0 3 * * 0` pour chaque dimanche.
- **Max @auto backups to keep** : le nombre d'archives automatiques conservées.

Le changement prend effet sans redémarrage.

### Envoyer les archives ailleurs (stockage compatible S3)

Toujours dans `/_/` → **Settings** → **Backup and restore** : cocher **Store
backups in S3 storage**, puis renseigner *endpoint*, *bucket*, *region*, *access
key* et *secret*. N'importe quel service compatible S3 convient — le bouton de
test de connexion à côté du formulaire dit tout de suite si c'est bon.

Il n'y a **rien à mettre dans l'environnement ni dans un fichier de
configuration** : les réglages PocketBase vivent dans la base, et c'est la seule
source de vérité.

> **Avertissement, à lire avant de renseigner une clé S3.** Sans l'option
> `--encryptionEnv`, les réglages — identifiants S3 compris — sont stockés **en
> clair** dans `data.db`. Or `data.db` est précisément ce que l'archive
> contient. Une archive envoyée sur S3 porte donc les identifiants de ce même
> S3 : quiconque met la main sur le bucket obtient de quoi y revenir.
>
> Pour les chiffrer, lancer le serveur avec une clé de 32 caractères. Cette clé
> se **tire au hasard**, et elle est **propre à votre installation** : une clé
> recopiée depuis une documentation est connue de tous ceux qui l'ont lue, et le
> chiffrement ne protège alors plus rien tout en paraissant actif.
>
> ```sh
> export PATACHOO_CLE_REGLAGES=$(head -c 24 /dev/urandom | base64 | cut -c1-32)
> echo "$PATACHOO_CLE_REGLAGES"   # à conserver ailleurs qu'ici, avant de continuer
> ./Patachoo serve --encryptionEnv=PATACHOO_CLE_REGLAGES
> ```
>
> La variable doit être présente à **chaque** démarrage, et la perdre rend les
> réglages illisibles. À défaut, réservez au bucket de sauvegarde des
> identifiants qui ne servent qu'à lui, en écriture seule si le service le
> permet.
>
> **Ce que l'option ne fait pas, et c'est le piège.** `--encryptionEnv` chiffre
> les *réglages*, et eux seuls — la table `_params`. Le reste de `data.db`
> n'est pas touché, et notamment les **secrets de signature des jetons**
> d'authentification, qui restent en clair dans `_collections.options`. Or ces
> secrets suffisent à fabriquer un jeton d'administration valable : la clé
> protège vos identifiants S3, elle ne met pas `data.db` à l'abri. Ce qui
> protège le fichier, c'est le mode sous lequel il est écrit — voir
> « `pb_data` n'est lisible que par le compte du serveur » plus haut.

### Sauvegarder à la main

Dans `/_/` → **Settings** → **Backup and restore** → **Initialize new backup**.
Le champ **Backup name** peut rester vide : PocketBase nomme alors l'archive
tout seul.

Par l'API, avec un jeton de superutilisateur. Le mot de passe se tape, il ne
s'écrit pas dans la commande. Lancez cette ligne **seule** et répondez à
l'invite : `read` lit sur le terminal, donc collée au milieu du bloc suivant
elle prendrait pour mot de passe la ligne d'après au lieu de vous interroger.

```sh
printf 'Mot de passe superutilisateur : '; read -rs MDP; echo
```

La frappe ne s'affiche pas et ne va pas dans l'historique. Le mot de passe une
fois tapé, le reste se colle d'un seul tenant :

```sh
: "${MDP:?mot de passe absent : tapez-le avec le bloc ci-dessus}"

JETON=$(curl -s -X POST http://127.0.0.1:8090/api/collections/_superusers/auth-with-password \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg mdp "$MDP" '{identity:"vous@exemple.fr",password:$mdp}')" | jq -r .token)

curl -s -X POST http://127.0.0.1:8090/api/backups \
  -H "Authorization: $JETON" -H 'Content-Type: application/json' \
  -d '{"name":"avant-mise-a-jour.zip"}'

curl -s http://127.0.0.1:8090/api/backups -H "Authorization: $JETON" | jq -r '.[].key'
```

La création répond `204` sans corps : c'est le succès, pas une réponse perdue.
Le nom doit correspondre à `^[a-z0-9_-]+\.zip$` — minuscules, chiffres, tirets
et soulignés, et l'extension `.zip`.

### Restaurer

La restauration **remplace** le contenu de `pb_data` : les recettes créées
depuis l'archive sont perdues. Prenez une sauvegarde manuelle juste avant, elle
sert de retour en arrière.

PocketBase écarte le `pb_data` courant, y déverse l'archive, puis **redémarre le
processus**. Si le redémarrage échoue, il revient tout seul à l'état précédent.
Une archive sans `data.db` est refusée. La restauration n'est pas supportée sous
Windows.

**Par l'interface d'administration.** `/_/` → **Settings** → **Backup and
restore** → le menu à droite de l'archive → **Restore**. La fenêtre de
confirmation demande de **recopier le nom de l'archive** : c'est volontaire, et
c'est le dernier moment où l'on peut se raviser. La page se reconnecte d'elle-même
une fois le serveur revenu.

**Par l'API.** Le mot de passe se tape à part, pour la raison dite plus haut —
cette ligne **seule**, d'abord :

```sh
printf 'Mot de passe superutilisateur : '; read -rs MDP; echo
```

Puis :

```sh
: "${MDP:?mot de passe absent : tapez-le avec le bloc ci-dessus}"

JETON=$(curl -s -X POST http://127.0.0.1:8090/api/collections/_superusers/auth-with-password \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg mdp "$MDP" '{identity:"vous@exemple.fr",password:$mdp}')" | jq -r .token)

curl -s -X POST http://127.0.0.1:8090/api/backups/avant-mise-a-jour.zip/restore \
  -H "Authorization: $JETON"
```

La réponse revient immédiatement (`204`) : le serveur attend une seconde, puis
restaure et redémarre. Comptez quelques secondes avant qu'il réponde de nouveau.

**Restaurer une archive qui vient d'ailleurs** — d'un stockage externe, d'un
autre serveur : la déposer d'abord, puis la restaurer.

```sh
curl -s -X POST http://127.0.0.1:8090/api/backups/upload \
  -H "Authorization: $JETON" -F 'file=@./avant-mise-a-jour.zip'
```

Ou, serveur arrêté, en déposant simplement le fichier dans `pb_data/backups/`.

**Vérifier que ça a marché.** Les recettes sont revenues, et leurs images
s'affichent — pas seulement leur titre. Une image manquante veut dire que
l'archive ne portait pas `storage/`.

### Éprouver la procédure

Une sauvegarde jamais restaurée n'est pas une sauvegarde, c'est une intention.
Éprouvez-la sur une **instance jetable**, jamais sur votre base de travail : la
restauration est destructive, et se tromper de terminal arrive.

Le scénario complet : une recette avec image, une sauvegarde, la recette
supprimée, la restauration, la recette de retour — image comprise.

Le mot de passe de cette instance est jetable, mais c'est le geste qui
s'apprend : il se tape à part, lui aussi. Cette ligne **seule**, d'abord :

```sh
printf "Mot de passe de l'instance jetable : "; read -rs MDP; echo
```

Puis :

```sh
: "${MDP:?mot de passe absent : tapez-le avec le bloc ci-dessus}"
ESSAI=$(mktemp -d)
echo "$ESSAI"   # recopiez ce chemin : le second terminal en aura besoin
go build -o "$ESSAI/patachoo" ./cmd/patachoo
"$ESSAI/patachoo" superuser upsert essai@exemple.fr "$MDP" --dir "$ESSAI/pb_data"
"$ESSAI/patachoo" serve --dir "$ESSAI/pb_data" --http 127.0.0.1:8137
```

Le `read` sort le mot de passe de l'historique du shell, pas de `ps` : la
variable est développée avant l'exécution, et `superuser upsert` la reçoit en
argument comme avant — la sous-commande vient de PocketBase et n'en prend pas
d'autre.

Dans un second terminal, en replaçant `ESSAI` et en redonnant le même mot de
passe — ce sont des variables de shell, elles ne franchissent pas la fenêtre, et
sans la première les commandes qui suivent viseraient `/pb_data`.

Le mot de passe d'abord — le même que dans le premier terminal, et cette ligne
**seule** :

```sh
printf "Mot de passe de l'instance jetable : "; read -rs MDP; echo
```

Puis, en replaçant `ESSAI` :

```sh
: "${MDP:?mot de passe absent : tapez-le avec le bloc ci-dessus}"
ESSAI=<le chemin affiché par le echo ci-dessus>
BASE=http://127.0.0.1:8137

JETON=$(curl -s -X POST "$BASE/api/collections/_superusers/auth-with-password" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg mdp "$MDP" '{identity:"essai@exemple.fr",password:$mdp}')" | jq -r .token)

# une recette avec son image
python3 -c "import base64,sys; sys.stdout.buffer.write(base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=='))" > "$ESSAI/tarte.png"
RECETTE=$(curl -s -X POST "$BASE/api/collections/recipes/records" \
  -H "Authorization: $JETON" \
  -F 'title=Tarte aux pommes' -F "image=@$ESSAI/tarte.png" | jq -r .id)

# la sauvegarde
curl -s -X POST "$BASE/api/backups" \
  -H "Authorization: $JETON" -H 'Content-Type: application/json' \
  -d '{"name":"repetition.zip"}'
curl -s "$BASE/api/backups" -H "Authorization: $JETON" | jq -r '.[].key'

# la perte
curl -s -X DELETE "$BASE/api/collections/recipes/records/$RECETTE" -H "Authorization: $JETON"
curl -s "$BASE/api/collections/recipes/records" -H "Authorization: $JETON" | jq '.totalItems'   # 0

# la restauration : le serveur redémarre, laissez-lui quelques secondes
curl -s -o /dev/null -w '%{http_code}\n' -X POST "$BASE/api/backups/repetition.zip/restore" \
  -H "Authorization: $JETON"
sleep 10

# la preuve : la recette est revenue, et son image se télécharge
JETON=$(curl -s -X POST "$BASE/api/collections/_superusers/auth-with-password" \
  -H 'Content-Type: application/json' \
  -d "$(jq -nc --arg mdp "$MDP" '{identity:"essai@exemple.fr",password:$mdp}')" | jq -r .token)
curl -s "$BASE/api/collections/recipes/records" -H "Authorization: $JETON" | jq -r '.totalItems, .items[0].title, .items[0].image'
IMAGE=$(curl -s "$BASE/api/collections/recipes/records" -H "Authorization: $JETON" | jq -r '.items[0].image')
curl -s -o /dev/null -w '%{http_code}\n' "$BASE/api/files/recipes/$RECETTE/$IMAGE"   # 200
```

Une fois convaincu : arrêter le serveur, puis `rm -rf "$ESSAI"`.

Un titre revenu mais une image en `404` veut dire que l'archive ne portait pas
`storage/` — c'est le seul échec de restauration qui se déguise en succès.

### Trois comportements qui surprennent

1. **La rotation ne concerne que les archives automatiques.** *Max @auto
   backups to keep* ne supprime que les fichiers préfixés `@auto_pb_backup_`. Une
   archive créée à la main n'est **jamais** effacée : elle s'accumule jusqu'à
   remplir le disque. Faites le ménage vous-même, dans `/_/` →
   **Settings** → **Backup and restore**, ou par `DELETE /api/backups/<nom>`.
2. **Le cron ne tourne que pendant que le serveur tourne.** Une instance éteinte
   trois semaines ne rattrape rien au redémarrage : il n'y aura simplement
   aucune archive pour ces trois semaines-là.
3. **Une sauvegarde qui échoue prévient par courriel les superutilisateurs** —
   donc par rien du tout tant que le SMTP n'est pas configuré, et il ne l'est
   pas par défaut. Le seul signal restant est la ligne `[Backup cron] Failed to
   create backup` dans les journaux du serveur. Tant que la supervision n'est
   pas en place, jeter un œil de temps en temps à `pb_data/backups/` reste la
   vérification la plus fiable : une archive datée d'hier, c'est que tout va
   bien.
