ARG BASE_UBUNTU=registry.deckhouse.io/base_images/ubuntu:jammy-20240808@sha256:e20b137325a45b9fe9f87ed718799a0728edabe05e88585f371e6864994cf0bc
ARG BASE_GOLANG_BULLSEYE=registry.deckhouse.io/base_images/golang:1.22.6-bullseye@sha256:260918a3795372a6d33225d361fe5349723be9667de865a23411b50fbcc76c5a

FROM $BASE_GOLANG_BULLSEYE as builder
ARG LINSTOR_SCHEDULER_EXTENDER_GITREPO=https://github.com/piraeusdatastore/linstor-scheduler-extender
ARG LINSTOR_SCHEDULER_EXTENDER_VERSION=0.3.2
ARG STORK_GITREPO=https://github.com/libopenstorage/stork
# the closest version I found for the stork's module version v1.4.1-0.20220512171133-b99428ee1ddf which used in linstor-scheduler-extender v0.3.2
# https://github.com/libopenstorage/stork/pull/1097/commits
ARG STORK_VERSION=v2.11.5

# Copy patches
COPY ./patches /patches

# Stork patching
RUN git clone ${STORK_GITREPO} /usr/local/go/stork \
 && cd /usr/local/go/stork \
 && git reset --hard ${STORK_VERSION} \
 && [ -n "$(ls -A /patches/stork/*.patch 2>/dev/null)" ] && git apply /patches/stork/*.patch || echo "Stork patches not found. Git patching skipped"

RUN git clone ${LINSTOR_SCHEDULER_EXTENDER_GITREPO} /usr/local/go/linstor-scheduler-extender \
 && cd /usr/local/go/linstor-scheduler-extender \
 && git reset --hard v${LINSTOR_SCHEDULER_EXTENDER_VERSION} \
 && [ -n "$(ls -A /patches/*.patch 2>/dev/null)" ] && git apply /patches/*.patch || echo "Patches not found. Git patching skipped" \
 && go mod edit -replace=github.com/libopenstorage/stork=/usr/local/go/stork \
 && go mod tidy \
 && cd cmd/linstor-scheduler-extender \
 && go build -ldflags="-X github.com/piraeusdatastore/linstor-scheduler-extender/pkg/consts.Version=v${LINSTOR_SCHEDULER_EXTENDER_VERSION}" \
 && mv ./linstor-scheduler-extender /

FROM $BASE_UBUNTU
COPY --from=builder /linstor-scheduler-extender /
USER nonroot
ENTRYPOINT ["/linstor-scheduler-extender"]
