#!/bin/sh
# Après désinstallation du paquet patachoo : systemd oublie l'unité retirée.
#
# Joué aussi après une mise à jour, sans dommage : relire les unités ne change
# rien à un service qui tourne. Le répertoire de données n'est pas touché,
# purge comprise.
#
# Sans systemctl, rien à faire ; un systemctl en échec ne fait pas échouer la
# transaction (voir apres-installation.sh).

systemctl_si_present() {
	command -v systemctl >/dev/null 2>&1 || return 0
	systemctl "$@"
}

systemctl_si_present daemon-reload

# Le code de sortie ne dépend d'aucun systemctl.
exit 0
