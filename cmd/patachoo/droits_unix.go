//go:build unix

package main

import "syscall"

// resserreLesDroits retire à group et à other tout droit sur ce que le serveur
// crée ensuite.
//
// Un seul appel, et il couvre tout : PocketBase crée pb_data et ses
// sous-répertoires avec os.ModePerm (0777), les bases et les archives de
// sauvegarde avec 0666 ou 0644. Ces modes ne sont pas réglables, mais ils
// passent tous par l'umask du processus — le poser ici resserre donc du même
// coup ce qu'on ne peut pas atteindre autrement.
//
// 0o077 et pas 0o027 : personne d'autre que le serveur n'a besoin de lire ces
// fichiers. Les images téléversées sont servies par le processus lui-même, par
// /api/files/…, et aucun mode d'installation documenté ne met un serveur tiers
// devant pb_data/storage.
//
// Ce que ça protège : data.db porte, dans _collections.options, les secrets de
// signature des jetons, que --encryptionEnv ne chiffre pas. Qui peut le lire
// fabrique un jeton superuser valable et entre dans /_/.
//
// Les fichiers déjà écrits gardent leurs droits : un umask ne vaut que pour ce
// qui vient après. Le rattrapage d'une instance existante est dans INSTALL.md.
func resserreLesDroits() {
	syscall.Umask(0o077)
}
