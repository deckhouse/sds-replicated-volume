# Stage 1: Copy source code
FROM alpine:3.20 AS src-artifact
WORKDIR /src
COPY api ./api
COPY images/drbd-build-server ./images/drbd-build-server
RUN echo "src artifact"

# Stage 2: Build Go application
FROM golang:1.23.6-alpine3.20 AS golang-artifact

# Copy source code from previous stage
COPY --from=src-artifact /src /src

# Build the application
WORKDIR /src/images/drbd-build-server/cmd
RUN go mod download && \
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o /drbd-build-server main.go && \
    chmod +x /drbd-build-server

# Stage 3: Runtime image with build tools for DRBD kernel module compilation
FROM debian:bullseye-slim

# Build-time DRBD source settings
ARG DRBD_REPO="https://github.com/LINBIT/drbd.git"

# Install build tools and dependencies
RUN export DEBIAN_FRONTEND=noninteractive && \
    apt-get update && \
    apt-get install -y \
        coreutils \
        flex \
        bison \
        coccinelle \
        gcc \
        make \
        bc \
        libssl-dev \
        libelf-dev \
        git \
        ca-certificates \
        tar \
        gzip \
        python3 \
        python3-dev \
        patch \
        perl \
        build-essential && \
    ln -sf /usr/bin/python3 /usr/bin/python && \
    rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/*

# Clone DRBD sources into the image at build time
RUN git clone --depth 1 "${DRBD_REPO}" /drbd && \
    cd /drbd && \
    git submodule update --init --recursive && \
    git rev-parse HEAD > /tmp/drbd_git_hash && \
    mkdir -p /drbd/drbd && \
    printf "GIT-hash:%s\n" "$(cat /tmp/drbd_git_hash)" > /drbd/drbd/.drbd_git_revision && \
    rm -rf /drbd/.git /tmp/drbd_git_hash

# Copy the built binary from golang-artifact stage
COPY --from=golang-artifact /drbd-build-server /usr/local/bin/drbd-build-server

# Create necessary directories
RUN mkdir -p /var/cache/drbd-build-server /tmp/drbd-builds

WORKDIR /drbd

EXPOSE 2021

ENTRYPOINT ["/usr/local/bin/drbd-build-server"]
CMD ["-addr", ":2021"]

LABEL distro="debian" \
      version="all"

