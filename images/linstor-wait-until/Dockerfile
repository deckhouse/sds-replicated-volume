TODO: remove this file after migrate to Werf!

ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20240808@sha256:e20b137325a45b9fe9f87ed718799a0728edabe05e88585f371e6864994cf0bc
ARG BASE_GOLANG_BULLSEYE=registry.deckhouse.io/base_images/golang:1.22.6-bullseye@sha256:260918a3795372a6d33225d361fe5349723be9667de865a23411b50fbcc76c5a

FROM $BASE_GOLANG_BULLSEYE as builder
ARG LINSTOR_WAIT_UNTIL_GITREPO=https://github.com/LINBIT/linstor-wait-until
ARG LINSTOR_WAIT_UNTIL_VERSION=0.2.1

# Copy patches
COPY ./patches /patches

RUN git clone ${LINSTOR_WAIT_UNTIL_GITREPO} /usr/local/go/linstor-wait-until \
 && cd /usr/local/go/linstor-wait-until \
 && git reset --hard v${LINSTOR_WAIT_UNTIL_VERSION} \
 && git apply /patches/*.patch \
 && go build \
 && mv ./linstor-wait-until /

FROM $BASE_UBUNTU
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      curl \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /linstor-wait-until /
ENTRYPOINT ["linstor-wait-until"]