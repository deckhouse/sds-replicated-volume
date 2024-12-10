FROM golang:1 AS builder

WORKDIR /go/src/saas
COPY . .
RUN make && mv ./saas /

FROM fedora:40
COPY --from=builder /saas /sbin
RUN dnf install -y coccinelle diffutils make gcc tar python3-devel && \
	dnf clean all && rm -rf /var/cache/dnf
RUN mkdir -p /var/cache/saas/patches /var/cache/saas/tarballs

EXPOSE 2020
CMD ["-addr", ":2020"]
ENTRYPOINT ["/sbin/saas"]