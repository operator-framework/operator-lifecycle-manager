# current openshift does not support multi-stage builds, have to build a fat image
FROM golang:1.11
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY . .
RUN make build && cp bin/olm /bin/olm && cp bin/catalog /bin/catalog && cp bin/package-server /bin/package-server

COPY deploy/chart/catalog_resources /var/catalog_resources

CMD ["/bin/olm"]
