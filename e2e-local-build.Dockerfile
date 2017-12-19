FROM golang:1.9 as builder
WORKDIR /go/src/github.com/coreos-inc/alm
COPY . .
RUN make build && cp bin/alm /bin/alm && cp bin/catalog /bin/catalog

FROM alpine:latest
WORKDIR /
COPY --from=builder /go/src/github.com/coreos-inc/alm/bin/alm /bin/alm
COPY --from=builder /go/src/github.com/coreos-inc/alm/bin/catalog /bin/catalog
COPY catalog_resources /var/catalog_resources

CMD ["/bin/alm"]