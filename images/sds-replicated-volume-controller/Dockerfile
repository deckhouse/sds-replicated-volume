ARG BASE_GOLANG_20_ALPINE=registry.deckhouse.io/base_images/golang:1.20.4-alpine3.18@sha256:5f403dd08db2f0b40d4416e29d3080eec41cd6cf53a05d5e4bcece3a5c7a8ce6

FROM $BASE_GOLANG_20_ALPINE as builder

WORKDIR /go/src

ADD go.mod .
ADD go.sum .

RUN go mod download

COPY . .

WORKDIR /go/src/cmd
RUN GOOS=linux GOARCH=amd64 go build -o stctrl

FROM --platform=linux/amd64 $BASE_GOLANG_20_ALPINE
COPY --from=builder /go/src/tools /tools
COPY --from=builder /go/src/cmd/stctrl /go/src/cmd/stctrl

CMD ["/go/src/cmd/stctrl"]
