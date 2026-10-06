#!/bin/sh
# Après installation ou mise à jour du paquet patachoo.
#
# dpkg appelle « configure <version précédente> », la version étant vide à la
# première installation ; rpm passe le nombre d'exemplaires installés, 1 à la
# première installation et 2 pendant une mise à jour.
#
# Ni enable ni start : l'hébergeant lance « systemctl enable --now patachoo »
# en sachant qu'il ouvre un port. Une mise à jour ne relance le service que
# s'il tournait.
#
# Sans systemctl — conteneur, chroot —, rien à faire. Un systemctl en échec ne
# fait pas échouer la transaction : un paquet à moitié configuré coûterait plus
# cher qu'un service relancé à la main.

systemctl_si_present() {
	command -v systemctl >/dev/null 2>&1 || return 0
	systemctl "$@"
}

systemctl_si_present daemon-reload

case "$1" in
configure) [ -n "$2" ] && mise_a_jour=1 ;;
[0-9]*) [ "$1" -ge 2 ] && mise_a_jour=1 ;;
esac

if [ -n "$mise_a_jour" ]; then
	systemctl_si_present try-restart patachoo.service
fi

# Le code de sortie ne dépend d'aucun systemctl.
exit 0
