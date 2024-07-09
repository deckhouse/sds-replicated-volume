ARG BASE_GOLANG_22_ALPINE=registry.deckhouse.io/base_images/golang:1.22.3-alpine@sha256:dbf216b880b802c22e3f4f2ef0a78396b4a9a6983cb9b767c5efc351ebf946b0

FROM $BASE_GOLANG_22_ALPINE as builder

WORKDIR /go/src

ADD go.mod .
ADD go.sum .

RUN go mod download

COPY . .

WORKDIR /go/src/cmd
RUN GOOS=linux GOARCH=amd64 go build -o stctrl

FROM --platform=linux/amd64 $BASE_GOLANG_22_ALPINE
COPY --from=builder /go/src/tools /tools
COPY --from=builder /go/src/cmd/stctrl /go/src/cmd/stctrl

CMD ["/go/src/cmd/stctrl"]
