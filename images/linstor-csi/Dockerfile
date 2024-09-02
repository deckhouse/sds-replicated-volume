ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20221130@sha256:c14c3b1242536729ce5227ff833144977b4e378723858fb73a4cf40ea6daaf6a
ARG BASE_GOLANG_BULLSEYE=registry.deckhouse.io/base_images/golang:1.22.6-bullseye@sha256:260918a3795372a6d33225d361fe5349723be9667de865a23411b50fbcc76c5a

FROM $BASE_GOLANG_BULLSEYE as builder
ARG LINSTOR_CSI_GITREPO=https://github.com/linbit/linstor-csi
ARG LINSTOR_CSI_VERSION=98544cadb6d111d27a86a11ec07de91b99704b82

# Copy patches
COPY ./patches /patches

RUN git clone ${LINSTOR_CSI_GITREPO} /usr/local/go/linstor-csi/ \
 && cd /usr/local/go/linstor-csi \
 && git reset --hard ${LINSTOR_CSI_VERSION} \
 && git apply /patches/*.patch \
 && cd cmd/linstor-csi \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-csi/pkg/driver.Version=v${LINSTOR_CSI_VERSION}" \
 && mv ./linstor-csi /

FROM $BASE_UBUNTU
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      xfsprogs \
      e2fsprogs \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/* \
 && ln -sf /proc/mounts /etc/mtab

COPY --from=builder /linstor-csi /
ENTRYPOINT ["/linstor-csi"]
