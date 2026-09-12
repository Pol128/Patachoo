# Sécurité

Ce fichier dit comment signaler une faille dans Patachoo, et ce qu'il advient
ensuite. Il ne décrit aucune vulnérabilité : c'est une porte d'entrée, pas un
journal.

## Signaler une faille

**Par le signalement privé de GitHub, et par lui seul.** Sur
[github.com/Pol128/Patachoo](https://github.com/Pol128/Patachoo), onglet
**Security**, bouton **Report a vulnerability**. Le fil reste privé entre vous
et le mainteneur.

**N'ouvrez pas d'issue publique pour une faille** : une issue est lisible par
tout le monde, y compris par qui voudrait s'en servir avant que le correctif
existe.

Aucune adresse de courriel n'est publiée ici, et il n'y a pas de canal de
repli. Le prix de ce canal unique est assumé : il faut un compte GitHub pour
signaler.

## Ce qu'un bon signalement contient

- **La version ou le commit** sur lequel le problème a été observé.
- **Les étapes de reproduction**, aussi précises que possible : la requête, la
  page, les données saisies, la configuration particulière s'il y en a une.
- **Ce que l'attaquant obtient** : lire les recettes d'un autre compte, prendre
  la main sur l'instance, faire sortir une requête du serveur… C'est ce qui
  permet de juger de la gravité, et c'est ce qui manque le plus souvent.

## Sous quel délai

**Aucun délai n'est promis** : ni accusé de réception, ni diagnostic, ni
correctif. Patachoo est tenu sur temps libre, et une réponse viendra au mieux.

C'est dit ainsi volontairement. Un engagement qu'on ne tient pas abîme
davantage la confiance que l'absence d'engagement, et il n'y a ici ni équipe ni
astreinte pour en tenir un.

## Quelles versions sont suivies

**La branche `main`, et elle seule.** Patachoo n'a pas de version publiée : le
dépôt ne porte aucun tag, et le [README](README.md) dit « en construction ». Il
n'y a donc pas de tableau de versions soutenues à donner — un correctif, s'il
vient, vient sur `main`.

## Ce qui n'est pas dans le périmètre

Ce que vous hébergez vous-même — configuration du proxy inverse, TLS, système
d'exploitation, sauvegardes — relève de votre installation et non de Patachoo,
et aucune prime ne récompense un signalement.
