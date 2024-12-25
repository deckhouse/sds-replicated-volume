ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20240808@sha256:e20b137325a45b9fe9f87ed718799a0728edabe05e88585f371e6864994cf0bc
ARG BASE_GOLANG_BULLSEYE=registry.deckhouse.io/base_images/golang:1.22.6-bullseye@sha256:260918a3795372a6d33225d361fe5349723be9667de865a23411b50fbcc76c5a

FROM $BASE_GOLANG_BULLSEYE as builder
ARG LINSTOR_SCHEDULER_EXTENDER_GITREPO=https://github.com/piraeusdatastore/linstor-scheduler-extender
ARG LINSTOR_SCHEDULER_EXTENDER_VERSION=0.3.2

RUN git clone ${LINSTOR_SCHEDULER_EXTENDER_GITREPO} /usr/local/go/linstor-scheduler-extender \
 && cd /usr/local/go/linstor-scheduler-extender \
 && git reset --hard v${LINSTOR_SCHEDULER_EXTENDER_VERSION} \
 && cd cmd/linstor-scheduler-admission \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-scheduler-extender/pkg/consts.Version=v${LINSTOR_SCHEDULER_EXTENDER_VERSION}" \
 && mv ./linstor-scheduler-admission /

FROM $BASE_UBUNTU
COPY --from=builder /linstor-scheduler-admission /
USER nonroot
ENTRYPOINT ["/linstor-scheduler-admission"]
