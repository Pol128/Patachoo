# Installation

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
go build -o "$ESSAI/patachoo" .
"$ESSAI/patachoo" superuser upsert essai@exemple.fr 'mot-de-passe-jetable-32' --dir "$ESSAI/pb_data"
"$ESSAI/patachoo" serve --dir "$ESSAI/pb_data" --http 127.0.0.1:8137
```

Dans un second terminal, avec la même variable `ESSAI` :

```sh
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
