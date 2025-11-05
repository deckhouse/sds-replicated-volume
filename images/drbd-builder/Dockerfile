# Build stage: compile Go application
# Using golang:1.24 or latest to satisfy go.mod requirement of go 1.24.2+
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY main.go ./

# Build application
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o drbd-builder main.go

# Runtime stage: image with build tools for DRBD kernel module compilation
FROM debian:bullseye-slim

# Install dependencies for DRBD kernel module compilation
# Based on requirements from DRBD build process and SPAAS service
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        # Build tools (as specified by user)
        flex \
        bison \
        coccinelle \
        gcc \
        make \
        # Additional compiler tools
        bc \
        libssl-dev \
        libelf-dev \
        # Git for cloning DRBD repository
        git \
        ca-certificates \
        # Archive tools for handling tar.gz files
        tar \
        gzip \
        # Python for DRBD build scripts
        python3 \
        python3-dev \
        # Additional build dependencies
        patch \
        perl \
        build-essential \
        # Runtime dependencies
        libc6-dev \
        && \
    # Create symlink for python (some scripts expect 'python' command)
    ln -sf /usr/bin/python3 /usr/bin/python && \
    # Clean up apt cache
    rm -rf /var/lib/apt/lists/* /var/cache/apt/archives/*

# Copy compiled binary from builder stage
COPY --from=builder /build/drbd-builder /usr/local/bin/drbd-builder

# Create directories for cache and temporary files
RUN mkdir -p /var/cache/drbd-builder /tmp/drbd-builds

# Set working directory
WORKDIR /drbd

# Expose default port (can be overridden with -addr flag)
EXPOSE 2020

# Run the application
ENTRYPOINT ["/usr/local/bin/drbd-builder"]

# Default command (can be overridden)
CMD ["-addr", ":2020"]

