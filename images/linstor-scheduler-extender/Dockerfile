ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20221130@sha256:c14c3b1242536729ce5227ff833144977b4e378723858fb73a4cf40ea6daaf6a
ARG BASE_GOLANG_19_BULLSEYE=registry.deckhouse.io/base_images/golang:1.19.3-bullseye@sha256:3d68e9eabd09f01f5851297902f2756ee2456a2e28b212e553315a3ddfcffe4b

FROM $BASE_GOLANG_19_BULLSEYE as builder
ARG LINSTOR_SCHEDULER_EXTENDER_GITREPO=https://github.com/piraeusdatastore/linstor-scheduler-extender
ARG LINSTOR_SCHEDULER_EXTENDER_VERSION=0.3.2

RUN git clone ${LINSTOR_SCHEDULER_EXTENDER_GITREPO} /usr/local/go/linstor-scheduler-extender \
 && cd /usr/local/go/linstor-scheduler-extender \
 && git reset --hard v${LINSTOR_SCHEDULER_EXTENDER_VERSION} \
 && cd cmd/linstor-scheduler-extender \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-scheduler-extender/pkg/consts.Version=v${LINSTOR_SCHEDULER_EXTENDER_VERSION}" \
 && mv ./linstor-scheduler-extender /

FROM $BASE_UBUNTU
COPY --from=builder /linstor-scheduler-extender /
USER nonroot
ENTRYPOINT ["/linstor-scheduler-extender"]
