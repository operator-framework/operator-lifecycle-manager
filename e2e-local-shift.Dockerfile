# current openshift does not support multi-stage builds, have to build a fat image
FROM golang:1.10
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY . .
RUN make build && cp bin/olm /bin/olm && cp bin/catalog /bin/catalog

COPY deploy/chart/catalog_resources /var/catalog_resources

CMD ["/bin/olm"]
