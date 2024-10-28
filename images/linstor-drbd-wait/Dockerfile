TODO: remove this file (and entry.sh) after migrate to Werf!
ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20240808@sha256:e20b137325a45b9fe9f87ed718799a0728edabe05e88585f371e6864994cf0bc

FROM $BASE_UBUNTU
COPY /entry.sh /

ENTRYPOINT ["/entry.sh"]
