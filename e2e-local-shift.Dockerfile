# current openshift does not support multi-stage builds, have to build a fat image
FROM golang:1.10
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY . .
RUN make build && cp bin/alm /bin/alm && cp bin/catalog /bin/catalog

COPY catalog_resources /var/catalog_resources

CMD ["/bin/alm"]
