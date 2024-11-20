ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20240808@sha256:e20b137325a45b9fe9f87ed718799a0728edabe05e88585f371e6864994cf0bc
ARG BASE_GOLANG_BULLSEYE=registry.deckhouse.io/base_images/golang:1.22.6-bullseye@sha256:260918a3795372a6d33225d361fe5349723be9667de865a23411b50fbcc76c5a

FROM $BASE_GOLANG_BULLSEYE as builder
ARG LINSTOR_AFFINITY_CONTROLLER_GITREPO=https://github.com/piraeusdatastore/linstor-affinity-controller
ARG LINSTOR_AFFINITY_CONTROLLER_VERSION=1.1.0

RUN git clone ${LINSTOR_AFFINITY_CONTROLLER_GITREPO} /usr/local/go/linstor-affinity-controller \
 && cd /usr/local/go/linstor-affinity-controller \
 && git reset --hard v${LINSTOR_AFFINITY_CONTROLLER_VERSION} \
 && cd cmd/linstor-affinity-controller \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-affinity-controller/pkg/consts.Version=v${LINSTOR_SCHEDULER_EXTENDER_VERSION}" \
 && mv ./linstor-affinity-controller /

FROM $BASE_UBUNTU
COPY --from=builder /linstor-affinity-controller /
ENTRYPOINT ["/linstor-affinity-controller"]
