# Les entrées de journal en attente de publication

Une tâche livrée n'écrit plus sa ligne directement dans `CHANGELOG.md` : elle
dépose **un fichier par tâche** dans ce répertoire, nommé d'après son
identifiant — `PATA-79.md`.

## Pourquoi

Parce que toutes les tâches écrivaient au même endroit : la première ligne de
la section « À paraître ». Deux branches ouvertes en même temps entraient donc
en conflit sur ce fichier, **même sans rien avoir en commun**. Mesuré le
12/09/2026 : cinq demandes de fusion en conflit, et `git merge-tree` ne rendait
qu'un seul fichier fautif, `CHANGELOG.md`.

Le coût n'était pas le conflit lui-même mais sa comptabilité. La review classe
un conflit en « corrigeable », ce qui consomme un aller-retour de correction sur
les deux autorisés : une tâche pouvait finir étiquetée `bloque`, en attente d'un
humain, **sur une ligne de journal**, sans qu'aucun avis n'ait jamais été porté
sur son code.

Deux tâches ne touchant jamais le même fichier, le conflit n'a plus lieu d'être.

## Ce qu'on écrit dedans

Le contenu du fichier est **exactement ce qui apparaîtra dans le journal** : une
ou plusieurs puces Markdown, en français, disant ce que l'utilisateur verra de
différent — pas le détail de l'implémentation.

```markdown
- La connexion et l'inscription ne lisent plus leurs champs que dans le corps
  du formulaire. Un mot de passe placé dans l'adresse n'ouvre plus de session.
```

## Ce qu'on en fait

    ./journal              # l'aperçu : ce que « À paraître » contiendra
    ./journal publier 0.2.0   # assemble les fragments dans CHANGELOG.md et les retire

L'aperçu est là parce que la section « À paraître » de `CHANGELOG.md` ne se
remplit plus au fil de l'eau : c'est le prix de ce découpage, et `./journal` le
rembourse en une commande.
