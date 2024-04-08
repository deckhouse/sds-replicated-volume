ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20221130@sha256:c14c3b1242536729ce5227ff833144977b4e378723858fb73a4cf40ea6daaf6a
ARG BASE_GOLANG_21_BULLSEYE=registry.deckhouse.io/base_images/golang:1.21.6-bullseye@sha256:a363973b58d4d5a91d6e8f700c847e87c1143459e55464fefca9e6135358af98

FROM $BASE_GOLANG_21_BULLSEYE as builder
ARG LINSTOR_AFFINITY_CONTROLLER_GITREPO=https://github.com/piraeusdatastore/linstor-affinity-controller
ARG LINSTOR_AFFINITY_CONTROLLER_VERSION=1.0.0

RUN git clone ${LINSTOR_AFFINITY_CONTROLLER_GITREPO} /usr/local/go/linstor-affinity-controller \
 && cd /usr/local/go/linstor-affinity-controller \
 && git reset --hard v${LINSTOR_AFFINITY_CONTROLLER_VERSION} \
 && cd cmd/linstor-affinity-controller \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-affinity-controller/pkg/consts.Version=v${LINSTOR_SCHEDULER_EXTENDER_VERSION}" \
 && mv ./linstor-affinity-controller /

FROM $BASE_UBUNTU
COPY --from=builder /linstor-affinity-controller /
ENTRYPOINT ["/linstor-affinity-controller"]
