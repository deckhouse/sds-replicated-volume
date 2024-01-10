ARG BASE_GOLANG_20_ALPINE=registry.deckhouse.io/base_images/golang:1.20.4-alpine3.18@sha256:5f403dd08db2f0b40d4416e29d3080eec41cd6cf53a05d5e4bcece3a5c7a8ce6
ARG BASE_GOLANG_20_ALPINE_BUILDER=$BASE_GOLANG_20_ALPINE

FROM $BASE_GOLANG_20_ALPINE_BUILDER as builder

WORKDIR /go/src

ADD go.mod .
ADD go.sum .

RUN go mod download

COPY . .

WORKDIR /go/src/cmd
RUN GOOS=linux GOARCH=amd64 go build -o rndcsi

FROM --platform=linux/amd64 $BASE_GOLANG_20_ALPINE
COPY --from=builder /go/src/cmd/rndcsi /go/src/cmd/rndcsi

ENTRYPOINT ["/go/src/cmd/rndcsi"]
