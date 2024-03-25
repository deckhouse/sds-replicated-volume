ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20221130@sha256:c14c3b1242536729ce5227ff833144977b4e378723858fb73a4cf40ea6daaf6a
ARG BASE_GOLANG_19_BULLSEYE=registry.deckhouse.io/base_images/golang:1.19.3-bullseye@sha256:3d68e9eabd09f01f5851297902f2756ee2456a2e28b212e553315a3ddfcffe4b

FROM $BASE_GOLANG_19_BULLSEYE as builder
ARG SPAAS_GITREPO=https://github.com/LINBIT/saas
ARG SPAAS_COMMIT_REF=7bef2e7976a455550bce2533487c635f20390ccf
ARG DRBD_GITREPO=https://github.com/LINBIT/drbd
ARG DRBD_VERSION=9.2.4

RUN git clone ${SPAAS_GITREPO} /usr/local/go/spaas \
 && cd /usr/local/go/spaas \
 && git reset --hard ${SPAAS_COMMIT_REF} \
 && go build -o /spaas

RUN apt-get update \
 && apt-get install -y make git \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

# Using source code from GitHub repository
RUN git clone ${DRBD_GITREPO} /drbd \
 && cd /drbd \
 && git reset --hard drbd-${DRBD_VERSION} \
 && make tarball

FROM $BASE_UBUNTU
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      gcc \
      libc6-dev \
      make \
      coccinelle \
      libpython3-dev \
 && update-alternatives --install /usr/bin/python python /usr/bin/python3 100 \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/* \
 && ln -sf /proc/mounts /etc/mtab
COPY --from=builder /spaas /
COPY --from=builder /drbd/drbd-*.tar.gz /var/cache/spaas/tarballs/
ENTRYPOINT ["/spaas"]
