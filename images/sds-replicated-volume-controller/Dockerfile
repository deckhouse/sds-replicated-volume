ARG BASE_GOLANG_ALPINE=golang:1.22.1-alpine@sha256:0de6cf7cceab6ecbf0718bdfb675b08b78113c3709c5e4b99456cdb2ae8c2495

FROM $BASE_GOLANG_ALPINE as builder

WORKDIR /go/src

ADD go.mod .
ADD go.sum .

RUN go mod download

COPY . .

WORKDIR /go/src/cmd
RUN GOOS=linux GOARCH=amd64 go build -o stctrl

FROM --platform=linux/amd64 $BASE_GOLANG_ALPINE
COPY --from=builder /go/src/tools /tools
COPY --from=builder /go/src/cmd/stctrl /go/src/cmd/stctrl

CMD ["/go/src/cmd/stctrl"]
