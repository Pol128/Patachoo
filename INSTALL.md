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

> **`/_/` et `/api/` ne sont pas deux pages de plus dans ce tableau : ce sont
> les clés de l'instance entière.** Elles répondent sur le **même port que le
> carnet**, donc sur les mêmes interfaces que lui — rien dans l'application ne
> les en sépare. Un mot de passe de superutilisateur accepté sur `/_/` donne la
> lecture et la modification de toutes les recettes et de tous les comptes, et
> le téléchargement d'une archive de tout `pb_data`.
>
> Elles ne doivent **jamais être joignables depuis l'Internet sans filtre
> devant**. Ouvrir une redirection de port vers 8090 sur sa box pour consulter
> ses recettes en déplacement, c'est publier ce formulaire de connexion
> d'administration sur l'Internet, en clair. La façon de s'y prendre est décrite
> plus bas, dans « Exposer Patachoo hors de chez soi ».

Dans le conteneur, Patachoo écoute sur `0.0.0.0:8090`, et cela ne change pas :
si 8090 est déjà pris sur la machine, c'est le **port de gauche** de
`"8090:8090"` que l'on modifie, par exemple `"8091:8090"`.

### Créer le premier superutilisateur

Rien n'est créé à l'avance : il faut un premier compte d'administration.

```sh
docker compose run --rm patachoo superuser upsert vous@exemple.fr 'un-mot-de-passe-solide'
```

Sur un conteneur déjà lancé, sans `compose` :

```sh
docker exec patachoo /patachoo superuser upsert vous@exemple.fr 'un-mot-de-passe-solide'
```

Au premier démarrage, les logs affichent aussi une URL à usage unique qui mène
au même résultat depuis le navigateur : `docker compose logs patachoo`.

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

## Exposer Patachoo hors de chez soi

Tant que Patachoo ne sert que la maison, le `docker-compose.yml` livré convient
tel quel et il n'y a rien à faire ici. Cette section est pour le cas d'après :
consulter ses recettes en déplacement, ou depuis un téléphone qui n'est pas sur
le Wi-Fi.

### Ce que publie `"8090:8090"`, exactement

Sans préfixe d'adresse, Docker publie le port sur **toutes les interfaces de la
machine hôte** — pas seulement sur celle du réseau local — et ouvre le passage
dans le pare-feu par sa propre règle, sans passer par `ufw` ou `firewalld`. Sur
un réseau domestique, c'est le comportement voulu : le carnet doit répondre au
reste de la maison.

Deux conséquences à avoir en tête avant d'aller plus loin :

- **Le trafic est en HTTP en clair.** Le mot de passe tapé dans le formulaire de
  connexion, celui du superutilisateur sur `/_/`, et le jeton qui en sort
  traversent le réseau **lisibles** par qui partage le segment. Sur un Wi-Fi
  familial, cela inclut le réseau invité et tout ce qui y est branché.
- **`/_/` répond sur ce même port**, comme dit plus haut. Une simple redirection
  de port depuis la box publie donc l'administration en même temps que le
  carnet.

Il ne faut donc **pas** rediriger le port 8090 de la box vers la machine. Ce
qu'il faut, c'est mettre un proxy inverse devant, qui termine le TLS et filtre
ce qui n'a rien à faire dehors.

### Un proxy inverse en HTTPS, avec Caddy

Caddy obtient et renouvelle son certificat Let's Encrypt tout seul, sans
commande à lancer ni tâche planifiée à poser ; c'est ce qui en fait l'exemple le
plus court. Il faut un nom de domaine qui pointe vers l'adresse publique, et les
ports 80 et 443 redirigés vers la machine — **80 et 443, pas 8090.**

Ce `Caddyfile` suffit :

```caddyfile
recettes.exemple.fr {
	reverse_proxy 127.0.0.1:8090
}
```

Le lancer, à côté de Patachoo :

```sh
docker run -d --name caddy --restart unless-stopped --network host \
    -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" -v caddy_data:/data \
    caddy:2
```

L'application est alors joignable en `https://recettes.exemple.fr`, et le mot de
passe ne circule plus en clair.

### Le compose, dans ce cas-là seulement

Une fois le proxy en place, le port 8090 n'a plus à être joignable directement :
seul le proxy doit y accéder. C'est **le seul cas** où l'on préfixe la
publication par une adresse :

```yaml
    ports:
      # Avec un proxy inverse devant, et dans ce cas seulement.
      - "127.0.0.1:8090:8090"
```

Ce n'est pas le nouveau défaut, et le `docker-compose.yml` de la racine ne
change pas : préfixé par `127.0.0.1`, il rendrait l'application injoignable
depuis le reste du réseau local, c'est-à-dire inutilisable pour l'installation
familiale que ce fichier vise. On ne fait ce changement qu'en même temps qu'on
installe le proxy, sous peine de ne plus rien joindre du tout.

### Fermer `/_/` au passage

Le proxy est aussi l'endroit où l'on décide que l'administration ne sort pas.
Elle n'a aucune raison d'être atteignable depuis l'extérieur : on ne s'y connecte
que de chez soi. Le filtre ci-dessous la rend à qui vient d'une adresse privée et
la fait disparaître pour tout le monde d'autre — ce qui la garde joignable depuis
la maison même après le passage à `"127.0.0.1:8090:8090"`, où le port 8090 n'est
plus atteignable directement.

```caddyfile
recettes.exemple.fr {
	# L'administration et le point d'authentification qui lui sert de porte :
	# joignables depuis le réseau local seulement. Les deux chemins
	# `/api/collections/…` désignent la même collection : PocketBase accepte
	# indifféremment son nom et son identifiant, et n'en oublier qu'un suffit
	# à rouvrir la porte.
	@administration path /_/* /api/collections/_superusers/* /api/collections/pbc_3142635823/*
	handle @administration {
		@interne remote_ip 192.168.0.0/16 10.0.0.0/8 172.16.0.0/12
		handle @interne {
			reverse_proxy 127.0.0.1:8090
		}
		respond 404
	}

	reverse_proxy 127.0.0.1:8090
}
```

`respond 404` plutôt que `403` : un 403 confirme que l'adresse existe, un 404 ne
dit rien. Ajuster les plages à celle du réseau — celles-ci couvrent les adresses
privées usuelles.

> **Les deux chemins `/api/collections/…` ne sont pas un doublon : ne pas en
> retirer un.** PocketBase désigne une collection *par son nom ou par son
> identifiant*, et sert la même route dans les deux cas. Or l'identifiant n'est
> pas tiré au sort à l'installation : il est calculé à partir du type et du nom
> de la collection, si bien que `_superusers` porte `pbc_3142635823` sur
> **toutes** les instances. Un filtre qui ne connaît que le nom se contourne
> donc en remplaçant `_superusers` par `pbc_3142635823` dans l'URL, et rend un
> jeton de superutilisateur à qui le demande depuis l'Internet.

Le filtre porte sur le **préfixe entier** de la collection, et pas sur la seule
route de connexion : `auth-with-password`, `request-password-reset` et
`auth-with-otp` s'y contournent toutes de la même façon.

`/api/` reste ouvert par ailleurs : c'est par lui que le carnet fonctionne. Ce
que la règle retire, c'est l'interface d'administration et **toutes les routes
par lesquelles un jeton de superutilisateur s'obtient**. Elle ne ferme pas les
adresses qu'un tel jeton déverrouille ensuite — `/api/settings`,
`/api/collections`, `POST /api/backups` répondent toujours —, mais PocketBase
les refuse à qui ne présente pas ce jeton, et il n'y a plus moyen d'en obtenir
un depuis l'extérieur.

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
> **En Docker**, qui est le mode d'installation décrit plus haut, l'option
> s'ajoute par un `command:` — et il faut alors **redonner en entier** celui que
> l'image porte (`serve`, `--http=0.0.0.0:8090`, `--dir=/pb_data`), car le
> déclarer le remplace : l'omettre ferait écouter le serveur sur `127.0.0.1`, où
> personne ne le joint depuis l'extérieur du conteneur, et écrire ses données
> ailleurs que dans le volume.
>
> ```yaml
> services:
>   patachoo:
>     image: ghcr.io/pol128/patachoo:latest
>     command:
>       - serve
>       - --http=0.0.0.0:8090
>       - --dir=/pb_data
>       - --encryptionEnv=PATACHOO_CLE_REGLAGES
>     environment:
>       PATACHOO_CLE_REGLAGES: la-cle-de-32-caracteres-tiree-plus-haut
> ```
>
> Mettre la clé dans le `docker-compose.yml` la met en clair dans un fichier
> qu'on sauvegarde et qu'on recopie ; `environment:` accepte aussi la forme
> `- PATACHOO_CLE_REGLAGES`, sans valeur, qui va alors la chercher dans
> l'environnement de `docker compose` ou dans un fichier `.env` à côté — à tenir
> hors des sauvegardes, puisque c'est précisément ce que la clé protège.
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

Par l'API, avec un jeton de superutilisateur :

```sh
JETON=$(curl -s -X POST http://127.0.0.1:8090/api/collections/_superusers/auth-with-password \
  -H 'Content-Type: application/json' \
  -d '{"identity":"vous@exemple.fr","password":"votre-mot-de-passe"}' | jq -r .token)

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

**Par l'API.**

```sh
JETON=$(curl -s -X POST http://127.0.0.1:8090/api/collections/_superusers/auth-with-password \
  -H 'Content-Type: application/json' \
  -d '{"identity":"vous@exemple.fr","password":"votre-mot-de-passe"}' | jq -r .token)

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

```sh
ESSAI=$(mktemp -d)
echo "$ESSAI"   # recopiez ce chemin : le second terminal en aura besoin
go build -o "$ESSAI/patachoo" ./cmd/patachoo
"$ESSAI/patachoo" superuser upsert essai@exemple.fr 'mot-de-passe-jetable-32' --dir "$ESSAI/pb_data"
"$ESSAI/patachoo" serve --dir "$ESSAI/pb_data" --http 127.0.0.1:8137
```

Dans un second terminal, en replaçant `ESSAI` — c'est une variable de
shell, elle ne franchit pas la fenêtre, et sans elle les commandes qui suivent
viseraient `/pb_data` :

```sh
ESSAI=<le chemin affiché par le echo ci-dessus>
BASE=http://127.0.0.1:8137
JETON=$(curl -s -X POST "$BASE/api/collections/_superusers/auth-with-password" \
  -H 'Content-Type: application/json' \
  -d '{"identity":"essai@exemple.fr","password":"mot-de-passe-jetable-32"}' | jq -r .token)

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
  -d '{"identity":"essai@exemple.fr","password":"mot-de-passe-jetable-32"}' | jq -r .token)
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
