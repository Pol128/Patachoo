# syntax=docker/dockerfile:1

# L'image de Patachoo : deux étapes, et une image finale qui ne contient que le
# binaire, le magasin de certificats, /tmp et /pb_data.
#
# FROM scratch n'est pas de la coquetterie : sans shell ni gestionnaire de
# paquets, il n'y a quasiment aucune surface de vulnérabilité à suivre, et rien
# à mettre à jour entre deux versions de Patachoo. Le prix à payer est écrit
# noir sur blanc dans ce fichier — pas de curl pour le HEALTHCHECK, pas de
# magasin de certificats fourni, pas de /tmp, et pas de `docker exec sh` pour
# aller voir ce qui se passe dedans.


# ─── Construction ────────────────────────────────────────────────────────────
#
# --platform=$BUILDPLATFORM : l'étape tourne sur l'architecture de l'hôte, quelle
# que soit la cible. CGO_ENABLED=0 rend les trois cibles atteignables par simple
# compilation croisée ; passer par QEMU coûterait dix fois le temps de
# construction sans rien apporter.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS construction

WORKDIR /src

# Les dépendances d'abord : elles changent bien moins souvent que le code, et
# cette couche-là se garde en cache d'une construction à l'autre.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Renseignés par buildx, une valeur par plateforme demandée. TARGETVARIANT vaut
# « v7 » pour linux/arm/v7 et rien pour les autres : GOARM prend ce qui suit le
# « v », donc 7 pour l'un et la valeur vide — ignorée — pour les autres.
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

# -trimpath retire les chemins de la machine de construction du binaire, -s -w
# ses tables de symboles et de débogage : c'est ce qui le ramène à 23 Mo.
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" GOARM="${TARGETVARIANT#v}" \
    go build -trimpath -ldflags "-s -w" -o /racine/patachoo .

# L'arborescence de l'image finale se prépare ici : dans un scratch, il n'y a
# aucun outil pour créer un répertoire ou en changer le propriétaire.
#
#   /tmp      — le routeur de PocketBase appelle ParseMultipartForm avec 16 Mio ;
#               un téléversement plus gros déborde dans os.TempDir(), qui
#               n'existerait pas. 1777 comme partout ailleurs.
#   /pb_data  — les données. Créé au bon UID pour qu'un volume nommé encore vide
#               en hérite : c'est ce qui fait qu'un docker compose up démarre
#               sans qu'on ait rien à faire côté droits.
RUN mkdir -p /racine/tmp /racine/pb_data \
 && chmod 1777 /racine/tmp \
 && chown 65532:65532 /racine/pb_data


# ─── Image finale ────────────────────────────────────────────────────────────
FROM scratch

# Le magasin de certificats. Sans lui, tout appel HTTPS sortant échoue : l'import
# d'une recette, la connexion par un fournisseur externe, la sauvegarde vers S3.
# Le golang:alpine le fournit ; s'il venait à ne plus le faire, ce COPY échoue —
# ce qui vaut mieux qu'une image qui se construit et ne sait plus parler TLS.
COPY --from=construction /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Le binaire, /tmp et /pb_data, avec les droits et le propriétaire posés à
# l'étape précédente.
COPY --from=construction /racine/ /

# La version des étiquettes vaut « dev » tant qu'on ne construit pas depuis un
# tag : --build-arg VERSION=0.1.0 la renseigne.
ARG VERSION=dev

LABEL org.opencontainers.image.title="Patachoo" \
      org.opencontainers.image.description="Gestionnaire de recettes auto-hébergé : on colle l'URL d'une recette, on obtient une fiche propre dans son propre carnet." \
      org.opencontainers.image.source="https://github.com/Pol128/Patachoo" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"

# Numérique, et non un nom : il n'y a pas de /etc/passwd dans un scratch pour
# traduire un nom en UID. 65532 est le « nonroot » des images distroless.
USER 65532:65532

EXPOSE 8090

# --http=0.0.0.0:8090 est obligatoire : le défaut de PocketBase est
# 127.0.0.1:8090, qui dans un conteneur ne répond à personne.
# --dir=/pb_data est explicite, pour qu'un `docker compose run` ne vise pas
# ailleurs selon l'endroit d'où il est lancé.
ENTRYPOINT ["/patachoo"]
CMD ["serve", "--http=0.0.0.0:8090", "--dir=/pb_data"]

# Forme exec, et pas la forme shell : celle-ci appellerait /bin/sh, qui n'existe
# pas ici. La sonde est une sous-commande du binaire lui-même — voir sante.go.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/patachoo", "healthcheck"]
