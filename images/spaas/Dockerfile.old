ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20240808@sha256:e20b137325a45b9fe9f87ed718799a0728edabe05e88585f371e6864994cf0bc
ARG BASE_GOLANG_BULLSEYE=registry.deckhouse.io/base_images/golang:1.22.6-bullseye@sha256:260918a3795372a6d33225d361fe5349723be9667de865a23411b50fbcc76c5a

FROM $BASE_GOLANG_BULLSEYE as builder
ARG SPAAS_GITREPO=https://github.com/LINBIT/saas
ARG SPAAS_COMMIT_REF=b22d7c3dbb8554af6739245d936a0dc5be01c748
ARG DRBD_GITREPO=https://github.com/LINBIT/drbd
ARG DRBD_VERSION=9.2.12

RUN git clone ${SPAAS_GITREPO} /usr/local/go/spaas \
 && cd /usr/local/go/spaas \
 && git reset --hard ${SPAAS_COMMIT_REF} \
 && go build -o /spaas

RUN apt-get update \
 && apt-get install -y make git jq \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

# Using source code from GitHub repository
RUN git clone ${DRBD_GITREPO} /drbd \
 && cd /drbd \
 && git checkout tags/drbd-${DRBD_VERSION}  \
 && git submodule update --init --recursive \
 && make tarball

FROM $BASE_UBUNTU
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      gcc \
      libc6-dev \
      make \
      coccinelle \
      diffutils \
      libpython3-dev \
      vim \
      jq \
 && update-alternatives --install /usr/bin/python python /usr/bin/python3 100 \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/* \
 && ln -sf /proc/mounts /etc/mtab
COPY --from=builder /spaas /
COPY --from=builder /drbd/drbd-*.tar.gz /var/cache/spaas/tarballs/
ENTRYPOINT ["/spaas"]
