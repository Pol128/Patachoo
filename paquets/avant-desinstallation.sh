#!/bin/sh
# Avant désinstallation du paquet patachoo — et pas avant une mise à jour.
#
# dpkg appelle « remove » pour une désinstallation, « upgrade » avant d'en
# remplacer la version ; rpm passe le nombre d'exemplaires qui resteront
# installés, 0 pour une désinstallation.
#
# Le répertoire de données n'est pas touché, purge comprise.
#
# Sans systemctl, rien à faire ; un systemctl en échec ne fait pas échouer la
# transaction (voir apres-installation.sh).

systemctl_si_present() {
	command -v systemctl >/dev/null 2>&1 || return 0
	systemctl "$@" || true
}

case "$1" in
remove | 0) systemctl_si_present disable --now patachoo.service ;;
esac

exit 0
