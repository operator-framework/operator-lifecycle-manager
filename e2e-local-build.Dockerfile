FROM golang:1.10 as builder
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY . .
RUN make build-coverage && cp bin/alm /bin/alm && cp bin/catalog /bin/catalog && bin/servicebroker /bin/servicebroker

FROM alpine:latest
WORKDIR /
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/alm /bin/alm
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/catalog /bin/catalog
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/servicebroker /bin/servicebroker
COPY catalog_resources /var/catalog_resources

CMD ["/bin/alm", "-h"]
