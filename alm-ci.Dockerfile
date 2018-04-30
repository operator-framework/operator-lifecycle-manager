FROM quay.io/coreos/alm-ci:base

WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager

# Cache Dep first
COPY Gopkg.toml Gopkg.lock Makefile ./
RUN make vendor

# Build bin
COPY . .
RUN make build && cp bin/alm /bin/alm

EXPOSE 8080
CMD ["/bin/alm"]
