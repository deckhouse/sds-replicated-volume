ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20221130@sha256:c14c3b1242536729ce5227ff833144977b4e378723858fb73a4cf40ea6daaf6a
ARG BASE_GOLANG_19_BULLSEYE=registry.deckhouse.io/base_images/golang:1.19.3-bullseye@sha256:3d68e9eabd09f01f5851297902f2756ee2456a2e28b212e553315a3ddfcffe4b

FROM $BASE_GOLANG_19_BULLSEYE as builder
ARG LINSTOR_CSI_GITREPO=https://github.com/linbit/linstor-csi
ARG LINSTOR_CSI_VERSION=1.2.2
ARG LINSTOR_WAIT_UNTIL_GITREPO=https://github.com/LINBIT/linstor-wait-until
ARG LINSTOR_WAIT_UNTIL_VERSION=0.2.1

# Copy patches
COPY ./patches /patches

RUN git clone ${LINSTOR_CSI_GITREPO} /usr/local/go/linstor-csi/ \
 && cd /usr/local/go/linstor-csi \
 && git reset --hard v${LINSTOR_CSI_VERSION} \
 && git apply /patches/*.patch \
 && cd cmd/linstor-csi \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-csi/pkg/driver.Version=v${LINSTOR_CSI_VERSION}" \
 && mv ./linstor-csi /

RUN git clone ${LINSTOR_WAIT_UNTIL_GITREPO} /usr/local/go/linstor-wait-until \
 && cd /usr/local/go/linstor-wait-until \
 && git reset --hard v${LINSTOR_WAIT_UNTIL_VERSION} \
 && go build \
 && mv ./linstor-wait-until /

FROM $BASE_UBUNTU
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      xfsprogs \
      e2fsprogs \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/* \
 && ln -sf /proc/mounts /etc/mtab

COPY --from=builder /linstor-csi /linstor-wait-until /
ENTRYPOINT ["/linstor-csi"]
