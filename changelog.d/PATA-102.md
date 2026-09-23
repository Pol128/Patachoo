- Patachoo s'installe désormais **en une commande** sur un Linux sans Docker ni
  Go : `installer.sh`, publié avec chaque Release, détecte la plateforme,
  télécharge l'archive, **compare sa somme** et pose le binaire — dans
  `/usr/local/bin` ou le préfixe choisi, pour la dernière version ou celle
  demandée. Il est lui-même couvert par les sommes et l'attestation de
  provenance : INSTALL.md montre d'abord comment le vérifier et le lire avant
  de le lancer. Il ne crée ni compte ni service, et renvoie macOS, Windows et
  les Raspberry Pi en armv6 vers la compilation depuis les sources.
