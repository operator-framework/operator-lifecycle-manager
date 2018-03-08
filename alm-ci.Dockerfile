FROM quay.io/coreos/alm-ci:base

# Cache Dep first
COPY Gopkg.toml Gopkg.lock Makefile ./
RUN make vendor

# Build bin
COPY . .
RUN make build && cp bin/alm /bin/alm

EXPOSE 8080
CMD ["/bin/alm"]
