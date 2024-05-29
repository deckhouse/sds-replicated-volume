ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20221130@sha256:c14c3b1242536729ce5227ff833144977b4e378723858fb73a4cf40ea6daaf6a
ARG BASE_GOLANG_21_BULLSEYE=registry.deckhouse.ru/base_images/golang:1.21.6-bullseye@sha256:a363973b58d4d5a91d6e8f700c847e87c1143459e55464fefca9e6135358af98

FROM $BASE_GOLANG_21_BULLSEYE as builder
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
