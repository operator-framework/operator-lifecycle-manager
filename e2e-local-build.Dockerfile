FROM golang:1.10 as builder
WORKDIR /go/src/github.com/coreos/alm
COPY . .
RUN make build-coverage && cp bin/alm /bin/alm && cp bin/catalog /bin/catalog && bin/servicebroker /bin/servicebroker

FROM alpine:latest
WORKDIR /
COPY --from=builder /go/src/github.com/coreos/alm/bin/alm /bin/alm
COPY --from=builder /go/src/github.com/coreos/alm/bin/catalog /bin/catalog
COPY --from=builder /go/src/github.com/coreos/alm/bin/servicebroker /bin/servicebroker
COPY catalog_resources /var/catalog_resources

CMD ["/bin/alm", "-h"]
