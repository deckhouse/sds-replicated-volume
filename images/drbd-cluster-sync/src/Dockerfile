FROM golang:1.20 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=unknown
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
    -ldflags "-X github.com/piraeusdatastore/linstor-csi/pkg/driver.Version=$VERSION -extldflags -static" \
    -o drbd-cluster-sync .

FROM alpine:latest

COPY --from=builder /app/drbd-cluster-sync /usr/local/bin/drbd-cluster-sync

ENTRYPOINT ["/usr/local/bin/drbd-cluster-sync"]
