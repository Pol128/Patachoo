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
`http://localhost:8090` si c'est la machine devant laquelle on est assis, et
`http://<adresse-de-la-machine>:8090` depuis un autre poste — mais **en clair,
cette seconde adresse ne permet pas de se connecter** : voir « Hors de la
machine locale : il faut du TLS », juste en dessous.

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

### Hors de la machine locale : il faut du TLS

**Le symptôme, d'abord**, parce que c'est par lui qu'on arrive ici. Depuis un
autre poste, sur `http://192.168.1.20:8090`, la page de connexion accepte les
identifiants sans broncher — et rend la page d'accueil **en visiteur**, comme si
rien ne s'était passé. Pas de message d'erreur, rien dans les journaux du
serveur. Réessayer donne la même chose.

**La cause** est que le cookie de session est posé avec le drapeau `Secure`
(`cmd/patachoo/session.go`), et qu'un navigateur n'enregistre un tel cookie que
s'il vient d'une **origine sûre** : `https://…`, ou bien `http://localhost` et
`http://127.0.0.1`, que les navigateurs traitent comme sûres par exception. Une
adresse IP de réseau local en clair n'en est pas une. Le cookie est donc jeté à
la réception, et la requête suivante repart anonyme — d'où la page d'accueil en
visiteur.

**Ne pas retirer `Secure`, et ne pas passer `SameSite` à `None`** pour faire
passer la connexion. Ce sont les deux pistes que le symptôme suggère, et les
deux sont des régressions de sécurité :

- sans `Secure`, le jeton de session voyage en clair sur le réseau, lisible par
  quiconque partage le Wi-Fi — et il vaut le compte, pas seulement le mot de
  passe ;
- `SameSite=None` fait tomber la seule défense CSRF du produit : les formulaires
  POST de Patachoo ne portent pas encore de jeton anti-rejeu, et c'est
  `SameSite=Lax` qui les protège.

C'est la documentation qui décrivait un déploiement impossible, pas le code qui
est trop strict. La suite donne les trois issues.

#### 1. Un proxy inverse qui termine le TLS — la voie normale

Patachoo ne change pas : il continue de servir en clair sur son 8090, et le
proxy, devant, porte le certificat. Avec [Caddy](https://caddyserver.com/), le
`Caddyfile` tient en trois lignes, certificat Let's Encrypt obtenu et renouvelé
tout seul :

```caddyfile
patachoo.exemple.fr {
	reverse_proxy 127.0.0.1:8090
}
```

Ce qu'il faut avoir avant : un **nom de domaine** qui pointe vers la machine, et
les ports **80 et 443** joignables depuis l'extérieur — c'est par eux que passe
la validation du certificat. Le proxy tournant sur la même machine, autant ne
plus publier 8090 sur tout le réseau : `"127.0.0.1:8090:8090"` dans le
`docker-compose.yml` le réserve à la boucle locale, donc au proxy.

**Un proxy inverse demande un second réglage, dans Patachoo cette fois.** La
connexion TCP que voit l'application ne vient plus du visiteur : elle vient du
proxy — Caddy comme nginx, Traefik ou l'ingress d'un NAS —, et **tous les
visiteurs partagent donc la même adresse aux yeux de Patachoo**. Sans le
réglage qui suit, le plafond de connexion se retourne contre eux.

**Le symptôme, avant le réglage.** La page de connexion refuse les gens avec
« trop de tentatives » alors qu'ils n'ont rien tenté. Le plafond posé sur
`POST /connexion` — cinq tentatives par 60 secondes, compté **par adresse IP** —
devient un compteur unique partagé par l'ensemble des visiteurs : cinq mauvais
mots de passe entrés n'importe où dans le monde, et plus personne ne se connecte
pendant la minute qui suit. Le symptôme ne désigne pas sa cause, c'est pour cela
qu'il est écrit ici : un hébergeant qui ne connaît pas ce piège cherche longtemps
du côté des comptes.

**Le réglage.** Dans `/_/` → **Settings** → **Application** → l'accordéon
**IP proxy headers** :

- **Trusted IP proxy headers** : le nom de l'en-tête que *votre* proxy pose.
  `X-Forwarded-For` pour Caddy, nginx et Traefik dans leur configuration
  courante ; `CF-Connecting-IP` derrière Cloudflare.
- **IP priority** : laissez **Use rightmost IP**, la valeur par défaut. L'en-tête
  peut porter plusieurs adresses séparées par des virgules ; la dernière est
  celle qu'a ajoutée le proxy le plus proche, donc la seule que vous contrôlez.
  *Use leftmost IP* fait lire la première, que le client peut préfixer lui-même —
  à ne choisir qu'en connaissance de cause, et seulement si votre chaîne de
  proxys l'impose.

Champ vide = mécanisme désactivé, et **c'est le défaut** : Patachoo ne pose
aucune valeur à l'installation.

> **Ne renseignez jamais ce champ sur une instance joignable en direct.** Le
> défaut vide n'est pas un oubli, c'est ce qui protège une installation sans
> proxy : tant qu'il est vide, un `X-Forwarded-For` forgé par un client est
> ignoré. Le renseigner rend l'en-tête croyable — n'importe qui en pose un,
> change d'adresse à chaque requête, et déplace le compteur à volonté. Le plafond
> de connexion ne compte alors plus rien, et on a remplacé un compteur trop large
> par un compteur qu'un attaquant choisit.
>
> La même prudence vaut si l'application reste atteignable **à la fois** par le
> proxy et en direct : fermez le port direct au pare-feu, ou réservez 8090 à la
> boucle locale comme ci-dessus.

**Vérifier que c'est bon.** Connecté en superutilisateur, `GET /api/health` rend
un champ `realIP` : après réglage, il doit porter **l'adresse du visiteur**, pas
celle du proxy.

```sh
JETON=$(curl -s -X POST https://patachoo.exemple.fr/api/collections/_superusers/auth-with-password \
  -H 'Content-Type: application/json' \
  -d '{"identity":"vous@exemple.fr","password":"votre-mot-de-passe"}' | jq -r .token)

curl -s https://patachoo.exemple.fr/api/health -H "Authorization: $JETON" | jq -r .data.realIP
```

La commande doit rendre l'adresse publique de la machine depuis laquelle vous la
lancez. Si elle rend l'adresse du proxy — souvent une adresse privée, `172.x.x.x`
dans un réseau Docker —, l'en-tête nommé n'est pas celui que votre proxy pose :
vérifiez sa configuration, puis corrigez le champ.

Sans jeton de superutilisateur, la réponse ne contient pas `realIP` : c'est
voulu, l'adresse n'est pas une information publique.

#### 2. Patachoo termine le TLS lui-même, sans rien devant

PocketBase sait obtenir son certificat seul : un nom de domaine passé en
argument de `serve` bascule les écoutes sur HTTP + HTTPS, obtient le certificat
par ACME et redirige le HTTP vers le HTTPS. Le `docker-compose.yml` complet :

```yaml
services:
  patachoo:
    image: ghcr.io/pol128/patachoo:latest
    restart: unless-stopped
    ports:
      - "80:8080"
      - "443:8443"
    command:
      - "serve"
      - "patachoo.exemple.fr"
      - "--http=0.0.0.0:8080"
      - "--https=0.0.0.0:8443"
      - "--dir=/pb_data"
    healthcheck:
      # Voir plus bas : la sonde de l'image interroge 127.0.0.1:8090, que ce
      # déploiement-ci ne sert pas.
      disable: true
    volumes:
      - pb_data:/pb_data

volumes:
  pb_data:
```

Trois choses à savoir sur ce fichier :

**Le certificat est rangé dans `pb_data/.autocert_cache`.** Il est donc
sauvegardé avec le reste, et un redémarrage ne relance pas une demande à Let's
Encrypt — qui plafonne les siennes.

**Les ports de gauche sont 80 et 443, ceux de droite 8080 et 8443**, et ce
décalage n'est pas de la coquetterie. À gauche, il n'y a pas le choix : c'est
sur les ports 80 et 443 que Let's Encrypt vient valider le domaine, et la
redirection vers le HTTPS que PocketBase installe pointe vers le port 443, sans
numéro de port explicite. À droite, écouter sous 1024 demanderait un réglage du
démon Docker, puisque l'image tourne en **UID 65532** — sur le démon où ceci a
été vérifié, `ip_unprivileged_port_start` vaut `0` et le bind sur 80 passe en
65532, mais ce n'est ni garanti ni universel (Docker sans racine, démon plus
ancien, sysctl durci). Écouter au-dessus de 1024 dans le conteneur marche
partout.

**La sonde de santé de l'image est désactivée**, et c'est une perte assumée.
Le `HEALTHCHECK` de l'image lance `/patachoo healthcheck` **sans argument**, et
cette sous-commande interroge alors son adresse par défaut, `127.0.0.1:8090`
(`adresseSanteDefaut`, `cmd/patachoo/sante.go`). Or ce `docker-compose.yml`
déplace les écoutes du conteneur sur 8080 et 8443 : plus rien ne sert 8090, et
la sonde échoue sur un refus de connexion — sans jamais rien recevoir de
l'application.

La repointer sur l'écoute en clair — `healthcheck --http=127.0.0.1:8080` — ne
réglerait rien non plus. Quand `--https` est actif, cette écoute-là ne sert plus
l'application : elle répond aux validations ACME et redirige tout le reste. La
sonde n'y récolterait qu'un `302` vers `https://127.0.0.1:443` — le gestionnaire
ACME réécrit le port à 443 plutôt que de le retirer —, qu'elle suivrait pour
échouer sur un second refus de connexion, le TLS du conteneur étant sur 8443.
Vérifié en rejouant la sonde contre ce gestionnaire.

Laissée en place, la sonde marquerait donc `unhealthy` un conteneur qui sert
parfaitement. La surveillance se fait alors depuis l'extérieur, sur
`https://patachoo.exemple.fr/api/health`.

#### 3. Sans nom de domaine : `http://localhost`, tunnel SSH compris

C'est l'issue de dépannage, celle qui ne demande ni certificat ni installation.
`http://localhost:8090` **depuis la machine elle-même** fonctionne, puisque le
navigateur y voit une origine sûre et garde le cookie.

Depuis un autre poste, un tunnel SSH donne la même chose, et pour la même
raison — le navigateur s'adresse à `localhost`, et ignore tout du reste du
chemin :

```sh
ssh -L 8090:localhost:8090 utilisateur@192.168.1.20
```

Le tunnel ouvert, `http://localhost:8090` dans le navigateur du poste distant
sert Patachoo, chiffré par SSH de bout en bout. C'est la voie à prendre un soir
de mise en service plutôt que d'aller toucher au cookie.

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

### Créer un compte ordinaire

Le superutilisateur ci-dessus administre l'instance ; il ne range pas les
recettes. Le carnet se tient avec un **compte ordinaire**, et une instance neuve
n'en porte aucun : elle démarre porte fermée, `/inscription` répond 404 et le
lien vers cette page ne figure même pas sur `/connexion`. Il y a deux chemins
pour ouvrir le premier, et ils ne supposent pas la même chose.

**Ce que donne un compte ordinaire**, par les deux chemins : le carnet sur `/`
— ses recettes, ses imports, ses commentaires. Pas `/_/` : l'administration
demande un superutilisateur et ne connaît pas les comptes de la collection
`users`.

**Avant le reste, depuis un autre poste que celui qui héberge : il faut du
TLS.** Sans lui, le navigateur jette le cookie de session, et ni la connexion ni
l'inscription ne tiennent — la page revient en visiteur, sans message d'erreur.
C'est expliqué plus haut, avec ses trois issues :
[Hors de la machine locale : il faut du
TLS](#hors-de-la-machine-locale--il-faut-du-tls).

#### 1. Le créer à la main depuis `/_/` — le chemin par défaut

L'instance reste porte fermée. Dans l'administration, collection `users`, *New
record* : le courriel, le mot de passe, et `name` pour le nom affiché. Le compte
est utilisable aussitôt sur `/connexion`.

C'est ce qu'on fait pour deux ou trois comptes. Son prix : **c'est vous qui
choisissez le mot de passe**, donc vous le connaissez, et il faut le transmettre
à son titulaire par un canal qui ne le laisse pas traîner.

#### 2. Ouvrir l'inscription, le temps qu'ils s'inscrivent, puis refermer

Cochez `open_registration` dans `/_/`, collection `settings`, l'unique
enregistrement, puis *Save*. Le réglage est **relu à chaque requête** : rien à
redémarrer. `/inscription` sert alors son formulaire — courriel, mot de passe,
confirmation, nom —, crée le compte et **connecte l'inscrit dans la foulée**.

Décochez la case une fois que les intéressés se sont inscrits. L'intérêt du
chemin est là : le mot de passe n'est connu que de son titulaire, il n'a transité
par personne.

Trois choses à savoir avant d'ouvrir :

- **Le mot de passe fait 8 caractères au minimum.** C'est le plancher du champ
  système de PocketBase ; Patachoo n'en ajoute pas.
- **Aucune vérification de courriel n'est demandée** avant la première
  connexion : rien n'envoie de courriel à l'inscription, et un compte non
  vérifié ouvre le carnet comme un autre. L'adresse saisie n'est donc pas une
  preuve d'identité.
- **`POST /inscription` est plafonné à dix requêtes par heure et par adresse
  IP**, tentatives ratées comprises — le limiteur compte les requêtes, pas les
  comptes créés. Une maisonnée derrière un même NAT qui s'inscrit à plusieurs,
  en se reprenant sur un mot de passe trop court ou un courriel déjà pris, peut
  donc toucher le plafond ; il se relâche tout seul au bout de l'heure.

Ouverte ou fermée, `POST /api/collections/users/records` reste refusé :
`users.createRule` est verrouillée au superutilisateur. L'inscription n'a qu'une
porte, et c'est celle-ci.

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

### Épingler une version, et vérifier ce qu'on a tiré

`:latest` est le chemin par défaut, et il le reste : le `docker-compose.yml`
ci-dessus doit marcher tel quel, déposé seul dans un répertoire vide. Ce qui
suit est une option, pour qui veut décider lui-même quand il monte de version,
ou s'assurer que l'image qu'il fait tourner vient bien de ce dépôt.

Chaque version publie trois références — `0.1.0`, `0.1` et `latest` — et une
pré-version (`0.2.0-rc.1`) ne déplace pas `latest`.

**Épingler par empreinte.** Un tag est mouvant : `latest`, et même `0.1`,
désignent une autre image après chaque publication. Une empreinte, non — c'est
le contenu lui-même qu'elle nomme :

```yaml
    image: ghcr.io/pol128/patachoo@sha256:0000000000000000000000000000000000000000000000000000000000000000
```

L'empreinte de la version visée se lit dans le registre, sans rien télécharger :

```sh
docker buildx imagetools inspect ghcr.io/pol128/patachoo:0.1.0
```

La ligne `Digest:` de la sortie est celle à recopier. Un `docker compose pull`
ne ramènera alors plus rien de nouveau : monter de version devient un geste
explicite — changer l'empreinte —, ce qui est tout l'intérêt.

**Vérifier la provenance.** L'image est construite et poussée par
[le workflow `publier`](.github/workflows/publier.yml), qui lui attache une
attestation de provenance. Elle se vérifie avec la commande `gh`, sans que
l'image ait à être tirée :

```sh
gh attestation verify oci://ghcr.io/pol128/patachoo:0.1.0 --repo Pol128/Patachoo
```

Ce que cela prouve : cette image a bien été produite par ce dépôt, par ce
workflow, à partir d'un commit nommé dans l'attestation — pas construite sur une
machine tierce ni substituée dans le registre après coup.

Ce que cela ne prouve pas : que le commit attesté soit digne de confiance. La
provenance dit d'où vient l'image, jamais ce que fait le code qu'elle contient.

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

Une seule exception : le déploiement où **Patachoo termine le TLS lui-même**. La
sonde vise `127.0.0.1:8090` par défaut, et ce déploiement-là déplace les écoutes
du conteneur sur 8080 et 8443 — plus rien ne répond sur 8090. Le
`docker-compose.yml` de
« Hors de la machine locale : il faut du TLS » la désactive pour cette raison,
et dit par quoi la remplacer.

## Installation par binaire

Patachoo est déjà un binaire unique, et l'image Docker n'est qu'un emballage
autour de lui : les gabarits et les fichiers statiques y sont embarqués par
`go:embed`, les migrations y sont compilées, et SQLite passe par une
implémentation en Go pur (`modernc.org`). Il n'y a donc **aucune dépendance
système à reproduire** — c'est ce que dit déjà le `FROM scratch` de l'image, qui
ne contient rien d'autre que cet exécutable.

Cette section est le chemin pour qui ne veut pas de démon Docker sur sa machine.

### Ce que ça change, et ce que ça ne change pas

Le binaire pèse **25 Mo**, et l'image `scratch` ne contient que lui : côté
serveur, la mémoire consommée est **la même dans les deux cas**. Un conteneur,
ce sont des espaces de noms, pas une machine virtuelle — il n'y a pas de système
invité à payer.

Le gain est **entièrement dans le démon**, et il dépend donc de ce que la
machine héberge par ailleurs :

- **Docker ne sert qu'à Patachoo.** On supprime `dockerd` et `containerd`, soit
  **~550 Mo** de mémoire résidente constatés, pour faire tourner 25 Mo de Go.
  Sur un VPS à 1 Go, c'est la différence entre à l'étroit et au large.
- **La machine héberge déjà d'autres conteneurs.** Le démon tourne de toute
  façon ; le coût marginal de Patachoo en conteneur se réduit au *shim*, de
  l'ordre de **17 Mo**. L'argument de la légèreté tombe, et le mode Docker
  reste le chemin le plus simple.

Le mode binaire n'est donc **pas universellement plus léger**. Ce qu'il apporte
dans tous les cas, en revanche, c'est de n'avoir aucun démon à administrer, et
une cible de compilation de plus — voir le tableau ci-dessous.

En échange, il faut **reconstruire à la main l'isolation** que le conteneur
donnait gratuitement : le `FROM scratch` et son `USER 65532:65532` enferment le
processus sans qu'on ait rien demandé. Un binaire lancé sous un compte ordinaire
est plus léger *et moins bien enfermé*. C'est l'unité systemd, plus bas, qui
rattrape cet écart ; elle n'est pas un bonus.

### Construire

Il faut **Go 1.26.6** — la version déclarée par `go.mod`. Rien d'autre : pas de
compilateur C, pas d'en-têtes de développement.

```sh
git clone https://github.com/Pol128/Patachoo.git
cd Patachoo
git checkout v0.1.0    # une version publiée, plutôt que la pointe de main
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=v0.1.0" -o patachoo ./cmd/patachoo
```

`-trimpath` retire du binaire les chemins de la machine qui l'a construit,
`-s -w` ses tables de symboles et de débogage — c'est ce qui le ramène à 25 Mo.

**La version se passe à la main, et c'est important.** Sans
`-X main.version=…`, le binaire annonce `dev` (`cmd/patachoo/version.go`) : dans
le pied de page d'un compte connecté comme dans `./patachoo version`. Construit
depuis un arbre git, il y ajoute la révision — `dev (1de2cd1)` —, ce qui vaut
mieux que rien mais ne dit toujours pas *quelle version* tourne. L'image Docker,
elle, reçoit la sienne par `--build-arg VERSION=` ; ici, personne ne le fait à
votre place.

#### Votre machine → ce que vous compilez

`CGO_ENABLED=0` rend toutes ces cibles atteignables par **simple compilation
croisée**, depuis n'importe quel poste. On compile chez soi, on copie les 25 Mo
sur la machine cible : celle-ci n'a besoin ni de Go, ni de Docker, ni d'aucune
bibliothèque.

| La machine qui fera tourner Patachoo | Ce qu'on passe à `go build` |
| --- | --- |
| PC, serveur ou VPS x86 64 bits | `GOARCH=amd64` |
| Raspberry Pi 3 / 4 / 5, Pi Zero 2 W, sous un OS 64 bits | `GOARCH=arm64` |
| Raspberry Pi 2, ou Pi 3 / 4 sous un OS 32 bits | `GOARCH=arm GOARM=7` |
| Raspberry Pi 1, Pi Zero / Zero W | `GOARCH=arm GOARM=6` |

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags "-s -w -X main.version=v0.1.0" -o patachoo ./cmd/patachoo
scp patachoo la-machine:/tmp/patachoo
```

**Ce chemin n'a pas de trou, là où l'image en a un.** `publier.yml` construit
`linux/amd64`, `linux/arm64` et `linux/arm/v7` — pas `arm/v6`. Un Raspberry Pi 1
ou un Pi Zero premier modèle n'a donc **aucune image Docker** à tirer, alors
qu'il a bien un binaire à compiler. C'est le seul avantage franc du mode binaire
sur le mode Docker, et il ne concerne que ces machines-là.

### Lancer

```sh
sudo install -m 0755 patachoo /usr/local/bin/patachoo
/usr/local/bin/patachoo serve --http=127.0.0.1:8090 --dir=/var/lib/patachoo
```

Deux drapeaux, et aucun des deux n'est facultatif.

**`--http`, sans quoi l'instance ne répond qu'à elle-même.** Le défaut de
PocketBase est `127.0.0.1:8090` : joignable depuis la machine elle-même, et de
nulle part ailleurs. Le `CMD` de l'image le surcharge en `0.0.0.0:8090`, ce qui
fait qu'on ne rencontre jamais la question en Docker ; un binaire lancé nu, non.
L'oubli ne produit aucune erreur — le serveur démarre, annonce son adresse, et
un navigateur d'un autre poste reçoit une connexion refusée. On cherche alors la
panne du côté du pare-feu, où elle n'est pas.

Ce qu'il faut y mettre dépend de ce qu'il y a devant : `127.0.0.1:8090` si un
proxy inverse tourne sur la même machine — le cas normal, et le seul recommandé
dès qu'on sort de chez soi —, `0.0.0.0:8090` pour exposer directement sur le
réseau local. Voir
[« Exposer Patachoo hors de chez soi »](#exposer-patachoo-hors-de-chez-soi).

**`--dir`, en chemin absolu, toujours.** Le défaut est `./pb_data`, *relatif au
répertoire courant*. Lancé à la main depuis deux répertoires différents, le
binaire ouvre deux bases différentes ; sous systemd avec un `WorkingDirectory=`
mal posé, les données atterrissent là où personne ne les cherche et l'instance a
l'air vide. Un chemin absolu supprime la question. L'unité ci-dessous le fait
poser par systemd lui-même.

### Le premier superutilisateur

Les deux chemins de
[« Créer le premier superutilisateur »](#créer-le-premier-superutilisateur)
valent ici, à la commande près. **Par le navigateur** reste le chemin conseillé :
au premier démarrage, tant qu'aucun superutilisateur n'existe, les logs du
serveur affichent une URL `/_/#/pbinstall/<jeton>` qui mène au formulaire de
création — le mot de passe se tape alors dans un champ, et ne passe ni par la
table des processus ni par l'historique du shell.

En repli, par la ligne de commande — serveur arrêté, puisque les deux processus
ouvriraient la même base :

```sh
patachoo superuser upsert vous@exemple.fr 'un-mot-de-passe-solide' --dir=/var/lib/patachoo
```

> L'avertissement de la section Docker s'applique **mot pour mot** : le mot de
> passe est ici un argument, donc lisible par `ps aux` pour tout utilisateur de
> la machine le temps de la commande, et conservé en clair dans
> `~/.bash_history` ou `~/.zsh_history` bien après. Les deux remèdes sont les
> mêmes — préfixer la ligne d'une espace si `HISTCONTROL` contient
> `ignorespace`, ou l'effacer avec `history -d <numéro>`.

Ce compte administre l'instance depuis `/_/` ; ce n'est pas celui avec lequel on
range ses recettes. Pour le compte ordinaire, rien ne change :
[« Créer un compte ordinaire »](#créer-un-compte-ordinaire) s'applique tel quel.

### L'unité systemd

Elle fait deux choses qu'on ne peut pas se contenter d'espérer : elle **relance
le service** après une panne — l'équivalent du `restart: unless-stopped` du
compose — et elle **réenferme le processus**, puisqu'il n'y a plus de conteneur
pour le faire.

Dans `/etc/systemd/system/patachoo.service` :

```ini
[Unit]
Description=Patachoo — carnet de recettes
Documentation=https://github.com/Pol128/Patachoo
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/patachoo serve --http=127.0.0.1:8090 --dir=/var/lib/patachoo
Restart=on-failure
RestartSec=5s

# Le compte : créé au démarrage, détruit à l'arrêt, il n'existe pas entre-temps.
# StateDirectory pose /var/lib/patachoo, le lui donne, et le conserve d'un
# démarrage à l'autre — c'est le chemin que --dir désigne ci-dessus, et les deux
# ne peuvent pas diverger sans que le service cesse de trouver ses données.
DynamicUser=yes
StateDirectory=patachoo
StateDirectoryMode=0700

# Le système de fichiers : tout en lecture seule sauf le répertoire d'état,
# /home et /root invisibles, un /tmp qui n'est partagé avec personne.
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

# Aucune élévation possible, aucune capacité conservée : Patachoo écoute
# au-dessus de 1024 et n'a besoin d'aucune.
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=

# Le noyau et le reste du système, hors de portée.
PrivateDevices=yes
ProtectClock=yes
ProtectHostname=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectControlGroups=yes
ProtectProc=invisible
RestrictNamespaces=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
LockPersonality=yes
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
SystemCallArchitectures=native
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now patachoo
systemctl status patachoo
journalctl -u patachoo -f
```

**`DynamicUser=yes` déplace les données, et il faut le savoir avant la première
sauvegarde.** Avec lui, systemd range l'état réel dans
`/var/lib/private/patachoo` et laisse `/var/lib/patachoo` comme lien
symbolique ; `/var/lib/private` n'est traversable que par `root`. Le service,
lui, ne voit que `/var/lib/patachoo` et n'en sait rien. Deux conséquences
pratiques : une sauvegarde lancée par un compte ordinaire échouera, et le
répertoire à viser depuis l'extérieur du service est
`/var/lib/private/patachoo`. Qui préfère un chemin franc remplace
`DynamicUser=yes` par un `User=patachoo` créé d'avance
(`sudo useradd --system --no-create-home --shell /usr/sbin/nologin patachoo`) :
`StateDirectory=` pose alors `/var/lib/patachoo` pour de bon, et le reste de
l'unité ne change pas.

`RestrictAddressFamilies=` garde `AF_INET` et `AF_INET6` parce que l'import va
chercher des pages sur Internet, et `AF_UNIX` parce que c'est par là que passe
la journalisation vers `journald`. Les retirer casserait l'un ou l'autre.

**Vérifier l'unité avant de la poser**, ce qui coûte une seconde :

```sh
systemd-analyze verify /etc/systemd/system/patachoo.service
```

#### La sonde de santé, hors Docker

`patachoo healthcheck` n'est pas propre au conteneur : c'est une sous-commande
ordinaire, qui interroge `/api/health` et rend **0** si l'application répond.
Elle vise `127.0.0.1:8090` par défaut et se règle par `--http` :

```sh
patachoo healthcheck --http=127.0.0.1:8090 ; echo $?
```

De quoi alimenter une supervision extérieure, ou un `ExecStartPost=` si l'on
tient à ce que `systemctl start` ne rende la main qu'une fois l'application
joignable. Elle n'ouvre pas le répertoire de données — son `--dir` est accepté
et ignoré —, on peut donc l'appeler aussi souvent qu'on veut.

#### Pourquoi un compte dédié, et pas le vôtre

Le binaire pose lui-même `umask(0o077)` au démarrage
(`cmd/patachoo/droits_unix.go`) : tout ce qu'il écrit ensuite — la base, les
fichiers téléversés, les archives de sauvegarde — n'est lisible que par le
compte qui l'exécute. C'est une protection réelle, et elle a une conséquence
directe sur le choix de ce compte.

Ce qu'elle protège : `data.db` porte, dans `_collections.options`, **les secrets
de signature des jetons**. Qui peut lire ce fichier peut fabriquer un jeton de
superutilisateur valable et entrer dans `/_/`. Lancer Patachoo sous son propre
compte d'utilisateur revient donc à ranger ces secrets parmi ses fichiers
personnels, à portée de tout ce qui tourne par ailleurs sous cette identité.
D'où le `DynamicUser=` ou le `User=` dédié de l'unité : une identité qui ne fait
que ça, et que personne n'emprunte.

### Sauvegarde

**`pb_data` reste la seule chose à conserver** — ici, le répertoire désigné par
`--dir`. La procédure ne change pas d'un mode à l'autre, et elle est écrite une
seule fois : voir
[« Sauvegarde et restauration »](#sauvegarde-et-restauration).

Ce qui disparaît en mode binaire, c'est toute l'histoire des droits de volume et
des *bind mounts* : il n'y a plus d'UID 65532 à faire correspondre, seulement un
répertoire appartenant au compte du service.

Ce qui ne change pas, en revanche : **pas de copie à chaud**. Une copie de
fichiers prise pendant que le serveur tourne donne une base corrompue, en mode
binaire exactement comme en Docker.

### Mettre à jour

Aucun `docker compose pull` ici, et pour une raison qu'il vaut mieux dire
franchement : **il n'existe aucun binaire publié**. `publier.yml` ne produit que
l'image `ghcr.io` et son attestation de provenance. La mise à jour consiste donc
à refaire soi-même ce qu'on a fait la première fois :

```sh
cd Patachoo
git fetch --tags
git checkout v0.2.0
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=v0.2.0" -o patachoo ./cmd/patachoo
sudo install -m 0755 patachoo /usr/local/bin/patachoo
sudo systemctl restart patachoo
```

Les données restent où elles sont ; seul l'exécutable est remplacé. Sauvegarder
`pb_data` avant une montée de version reste la précaution d'usage.

**Ce que la vérification devient ici.** En Docker, on vérifie une empreinte et
une attestation — « cette image vient bien de ce dépôt ». En mode binaire, on ne
vérifie rien de tel, puisqu'on ne télécharge rien : on **compile soi-même depuis
un tag du dépôt**, et c'est la confiance accordée au dépôt qui remplace celle
accordée au registre. Ce n'est pas moins sûr, c'est déplacé ailleurs.

### Exposer hors de chez soi

Rien ne change. `/_/` et `/api/` répondent sur **le même port que le site**,
comme en Docker, et tout ce que dit
[« Exposer Patachoo hors de chez soi »](#exposer-patachoo-hors-de-chez-soi)
s'applique à l'identique — le proxy inverse, le TLS, et ce que le proxy doit
fermer au passage. L'absence de conteneur ne change ni ce qui est publié, ni ce
qu'il faut filtrer devant.

Une seule différence de forme : il n'y a plus de `ports:` à écrire dans un
compose pour restreindre la publication. C'est `--http` qui joue ce rôle, et
`--http=127.0.0.1:8090` est l'équivalent exact de `"127.0.0.1:8090:8090"`.

## Exposer Patachoo hors de chez soi

Tant que Patachoo ne sert que la maison, le `docker-compose.yml` livré convient
tel quel et il n'y a rien à faire ici. Cette section est pour le cas d'après :
consulter ses recettes en déplacement, ou depuis un téléphone qui n'est pas sur
le Wi-Fi.

Elle prolonge [« Hors de la machine locale : il faut du
TLS »](#hors-de-la-machine-locale--il-faut-du-tls), qui donne les trois façons
d'obtenir du HTTPS. Ce qui suit dit ce que l'on publie au juste en sortant de
chez soi, et ce que le proxy doit retenir en plus de terminer le TLS.

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

### Le proxy, et ce qu'il doit fermer au passage

Le `Caddyfile` de trois lignes de « Hors de la machine locale » suffit à obtenir
le HTTPS ; il ne suffit pas à sortir de chez soi, parce qu'il publie aussi
l'administration. Le proxy est l'endroit où l'on décide qu'elle ne sort pas :
elle n'a aucune raison d'être atteignable depuis l'extérieur, on ne s'y connecte
que de chez soi. Le filtre ci-dessous la rend à qui vient d'une adresse du réseau
local et la fait disparaître pour tout le monde d'autre — ce qui la garde joignable depuis
la maison même après le passage à `"127.0.0.1:8090:8090"`, où le port 8090 n'est
plus atteignable directement.

C'est ce `Caddyfile`-ci, complet, qu'il faut prendre pour un déploiement exposé :

```caddyfile
recettes.exemple.fr {
	# L'administration et le point d'authentification qui lui sert de porte :
	# joignables depuis le réseau local seulement. Les deux chemins
	# `/api/collections/…` désignent la même collection : PocketBase accepte
	# indifféremment son nom et son identifiant, et n'en oublier qu'un suffit
	# à rouvrir la porte.
	@administration path /_/* /api/collections/_superusers/* /api/collections/pbc_3142635823/*
	handle @administration {
		# Les plages du réseau local, IPv4 **et** IPv6 : sans les secondes,
		# un navigateur de la maison qui préfère l'IPv6 reçoit un 404 sur
		# `/_/`. Une maison dont le fournisseur délègue un préfixe
		# globalement routable doit y ajouter le sien — voir sous le bloc.
		@interne remote_ip 192.168.0.0/16 10.0.0.0/8 172.16.0.0/12 127.0.0.1/32 ::1 fd00::/8 fe80::/10
		handle @interne {
			reverse_proxy 127.0.0.1:8090
		}
		respond 404
	}

	reverse_proxy 127.0.0.1:8090
}
```

Le lancer, à côté de Patachoo — Caddy obtient et renouvelle son certificat Let's
Encrypt tout seul, sans commande à lancer ni tâche planifiée à poser :

```sh
docker run -d --name caddy --restart unless-stopped --network host \
    -v "$PWD/Caddyfile:/etc/caddy/Caddyfile:ro" -v caddy_data:/data \
    caddy:2
```

L'application est alors joignable en `https://recettes.exemple.fr`, et le mot de
passe ne circule plus en clair.

`respond 404` plutôt que `403` : un 403 confirme que l'adresse existe, un 404 ne
dit rien.

**Ajuster les plages à celles du réseau.** Les trois premières couvrent les
adresses privées **IPv4** usuelles ; les suivantes sont leurs équivalents
**IPv6** — la boucle locale, les adresses locales uniques (`fd00::/8`, la moitié
de `fc00::/7` qui est effectivement attribuée sur place) et le lien-local. Sans
elles, le filtre ne répond qu'en IPv4 : dès que le nom porte un enregistrement
AAAA, ou que le réseau local est en IPv6, le navigateur de la maison préfère
l'IPv6 et son adresse source ne correspond alors à aucune plage.

**Une maison dont le fournisseur délègue un préfixe globalement routable doit y
ajouter le sien.** Il n'existe pas, en IPv6, d'équivalent de `192.168.0.0/16` :
les postes du logement portent des adresses publiques, tirées du préfixe délégué
à la box — c'est le cas courant chez un fournisseur d'accès grand public, et
aucune liste écrite d'avance ne peut le deviner. Le relever une fois (`ip -6
addr` sur un poste de la maison, ou l'interface de la box) et l'ajouter à
`@interne`.

> **Un 404 sur `/_/` *depuis la maison* veut dire que l'adresse source n'est pas
> dans les plages** — pas que l'administration est cassée. Le 404 ayant été
> choisi pour ne rien dire, il ne le dira pas. Le geste est d'ajouter sa plage à
> `@interne` ; **jamais** de retirer le bloc `@administration`, qui rouvrirait à
> l'Internet entier exactement ce que cette section ferme.

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
