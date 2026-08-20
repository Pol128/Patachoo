# Installer Patachoo

Patachoo tient dans un binaire unique : pas de base de données à installer à
côté, pas de serveur d'applications à configurer devant. Toutes ses données
vivent dans un seul répertoire, `pb_data`.

Un seul mode d'installation est décrit ici pour l'instant ; les autres
s'ajouteront avec leurs tâches.

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
