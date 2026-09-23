#!/bin/sh
set -eu

# Installe le binaire de Patachoo publié dans une Release GitHub.
#
# Lisez-le avant de le lancer : il est écrit pour ça. Il fait quatre choses, et
# rien d'autre :
#
#   1. détecter le système et l'architecture (uname -s, uname -m) ;
#   2. télécharger l'archive correspondante et le fichier de sommes publiés
#      avec la Release ;
#   3. comparer la somme SHA-256 de l'archive à celle que ce fichier annonce ;
#   4. poser le binaire dans le préfixe — /usr/local/bin par défaut.
#
# Il ne crée aucun utilisateur, ne dépose aucune unité de service, n'active ni
# ne démarre rien, n'ouvre aucun port et n'écrit nulle part ailleurs que dans le
# préfixe et dans son répertoire temporaire. Ces gestes-là se font à la main,
# en voyant ce qu'on pose : INSTALL.md, section « L'unité systemd ».
#
# Il ne lit jamais l'entrée standard : piper ce script dans un shell en fait
# l'entrée standard, et une question y consommerait ses propres lignes.
#
# Usage :
#   sh ./installer.sh [--prefix <répertoire>] [--version <vX.Y.Z>] [--force]
#
#   --prefix   où poser le binaire (défaut : /usr/local/bin)
#   --version  la version à installer, par son tag (défaut : la dernière)
#   --force    remplacer un patachoo déjà présent dans le préfixe
#
# PATACHOO_URL_BASE désigne un autre emplacement des Releases — un miroir, ou
# le serveur local des tests. La somme y est vérifiée comme partout ailleurs.

URL_BASE=${PATACHOO_URL_BASE:-https://github.com/Pol128/Patachoo/releases}
DOC_SYSTEMD="https://github.com/Pol128/Patachoo/blob/main/INSTALL.md#lunité-systemd"
DOC_CONSTRUIRE="https://github.com/Pol128/Patachoo/blob/main/INSTALL.md#construire"

prefixe=/usr/local/bin
version=
force=0

erreur() {
	printf 'installer.sh : %s\n' "$*" >&2
}

echec() {
	erreur "$@"
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--prefix)
		[ $# -ge 2 ] || echec "--prefix attend un répertoire"
		prefixe=$2
		shift 2
		;;
	--version)
		[ $# -ge 2 ] || echec "--version attend un tag, par exemple v0.3.0"
		version=$2
		shift 2
		;;
	--force)
		force=1
		shift
		;;
	*)
		echec "option inconnue : $1 (voir l'en-tête du script)"
		;;
	esac
done

# ─── 1. La plateforme ────────────────────────────────────────────────────────
#
# La table ne nomme que ce que .github/workflows/publier.yml publie :
# linux_amd64, linux_arm64, linux_armv7. Tout le reste est renvoyé vers la
# construction depuis les sources — en code 0 : une plateforme non distribuée
# n'est pas un échec du script, c'est l'autre chemin d'installation.

systeme=$(uname -s)
machine=$(uname -m)

# non_distribuee <plateforme nommée> <variables go build>
non_distribuee() {
	cat <<FIN
Aucun binaire de Patachoo n'est distribué pour cette plateforme : $1.
Rien n'a été téléchargé ni installé.

Patachoo se compile pour elle depuis les sources, avec Go :

    git clone https://github.com/Pol128/Patachoo.git && cd Patachoo
    CGO_ENABLED=0 $2 go build -trimpath -o patachoo ./cmd/patachoo

La marche à suivre complète : $DOC_CONSTRUIRE
FIN
	exit 0
}

# L'architecture telle que go build la nomme, pour la ligne de construction.
case "$machine" in
x86_64 | amd64) goarch="GOARCH=amd64" ;;
aarch64 | arm64) goarch="GOARCH=arm64" ;;
armv7* | armv8l) goarch="GOARM=7 GOARCH=arm" ;;
armv6*) goarch="GOARM=6 GOARCH=arm" ;;
armv5*) goarch="GOARM=5 GOARCH=arm" ;;
i?86) goarch="GOARCH=386" ;;
*) goarch="GOARCH=<votre architecture>" ;;
esac

case "$systeme" in
Linux) ;;
Darwin) non_distribuee "macOS ($systeme $machine)" "GOOS=darwin $goarch" ;;
MINGW* | MSYS* | CYGWIN* | Windows*)
	non_distribuee "Windows ($systeme $machine)" "GOOS=windows $goarch"
	;;
*) non_distribuee "$systeme $machine" "GOOS=<votre système> $goarch" ;;
esac

case "$machine" in
x86_64) cible=amd64 ;;
aarch64 | arm64) cible=arm64 ;;
armv7* | armv8l) cible=armv7 ;;
*) non_distribuee "Linux $machine" "GOOS=linux $goarch" ;;
esac

archive="patachoo_linux_$cible.tar.gz"

# ─── Les outils ──────────────────────────────────────────────────────────────

if command -v curl >/dev/null 2>&1; then
	telecharger() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	telecharger() { wget -q -O "$2" "$1"; }
else
	echec "il faut curl ou wget pour télécharger, et aucun des deux n'est installé"
fi

if command -v sha256sum >/dev/null 2>&1; then
	calculer_somme() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	calculer_somme() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	echec "il faut sha256sum ou shasum pour vérifier la somme, et aucun des deux n'est installé"
fi

# ─── Le préfixe ──────────────────────────────────────────────────────────────
#
# Vérifié avant tout téléchargement. Jamais d'élévation de privilèges ici :
# c'est à celui qui lance le script d'en décider, en le voyant.

if [ ! -d "$prefixe" ]; then
	echec "le préfixe $prefixe n'existe pas — créez-le, ou passez --prefix vers un répertoire existant"
fi
if [ ! -w "$prefixe" ]; then
	erreur "le préfixe $prefixe n'est pas accessible en écriture pour $(id -un 2>/dev/null || echo 'ce compte')."
	erreur "Deux issues : relancer ce script en root, ou passer --prefix vers un répertoire"
	erreur "accessible, par exemple --prefix \"\$HOME/.local/bin\"."
	exit 1
fi

cible_finale="$prefixe/patachoo"

# ─── 2. Le téléchargement ────────────────────────────────────────────────────
#
# Tout va dans un répertoire temporaire, supprimé en sortie quoi qu'il arrive.
# Le fichier de pose, lui, est créé dans le préfixe — même système de fichiers
# que la cible, pour que le déplacement final soit un renommage — et supprimé
# de même s'il n'est pas allé au bout.

temporaire=$(mktemp -d)
pose=
nettoyer() {
	rm -rf "$temporaire"
	if [ -n "$pose" ]; then rm -f "$pose"; fi
}
trap nettoyer EXIT
trap 'nettoyer; exit 1' INT TERM

if [ -n "$version" ]; then
	depuis="$URL_BASE/download/$version"
else
	depuis="$URL_BASE/latest/download"
fi

telecharger "$depuis/$archive" "$temporaire/$archive" ||
	echec "téléchargement impossible : $depuis/$archive"
telecharger "$depuis/sommes-sha256.txt" "$temporaire/sommes-sha256.txt" ||
	echec "téléchargement impossible : $depuis/sommes-sha256.txt"

# ─── 3. La somme ─────────────────────────────────────────────────────────────
#
# Comparée, pas seulement téléchargée. Elle prouve que l'archive est celle que
# la Release annonce ; d'où vient la Release, c'est la provenance qui le dit
# (INSTALL.md, « Vérifier la provenance »).

attendue=$(awk -v nom="$archive" '$2 == nom { print $1; exit }' \
	"$temporaire/sommes-sha256.txt")
[ -n "$attendue" ] || echec "le fichier de sommes n'annonce aucune somme pour $archive"
obtenue=$(calculer_somme "$temporaire/$archive")

if [ "$obtenue" != "$attendue" ]; then
	erreur "la somme SHA-256 de $archive ne correspond pas à celle que la Release annonce."
	erreur "  annoncée : $attendue"
	erreur "  obtenue  : $obtenue"
	erreur "Rien n'a été installé."
	exit 1
fi

tar -xzf "$temporaire/$archive" -C "$temporaire" patachoo ||
	echec "archive illisible : $archive"

# version_de <binaire> : ce qu'annonce `patachoo version`. Sans --dir, le
# binaire ouvrirait sa base dans ./pb_data, c'est-à-dire dans le répertoire de
# qui lance ce script ; lancé depuis le répertoire temporaire du système, sans
# --dev=false, PocketBase se croirait sous `go run` et imprimerait ses requêtes
# SQL avec la version. Les deux se sont vus en jouant le script à la main.
version_de() {
	"$1" version --dev=false --dir "$temporaire/pb_data" </dev/null
}

visee=$(version_de "$temporaire/patachoo") ||
	echec "le binaire téléchargé ne s'exécute pas sur cette machine"

# ─── 4. La pose ──────────────────────────────────────────────────────────────

if [ -e "$cible_finale" ] && [ "$force" -ne 1 ]; then
	presente=$(version_de "$cible_finale" 2>/dev/null || echo "inconnue")
	erreur "un patachoo est déjà installé : $cible_finale"
	erreur "  version présente : $presente"
	erreur "  version visée    : $visee"
	erreur "Rien n'a été changé. Relancez avec --force pour le remplacer."
	exit 1
fi

pose="$prefixe/.patachoo.installation.$$"
cp "$temporaire/patachoo" "$pose"
chmod 0755 "$pose"
mv -f "$pose" "$cible_finale"
pose=

installee=$(version_de "$cible_finale")
cat <<FIN
Patachoo $installee est installé : $cible_finale

La suite — lancer le serveur, le compte dédié, l'unité systemd — se fait à la
main, et ce script n'en fait rien. Elle est décrite dans INSTALL.md, section
« L'unité systemd » :
    $DOC_SYSTEMD
FIN
