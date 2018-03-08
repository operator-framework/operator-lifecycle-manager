FROM quay.io/coreos/alm-ci:base

# Cache Dep first
COPY Gopkg.toml Gopkg.lock Makefile ./
RUN make vendor

# Build bin
COPY . .
RUN make build && cp bin/servicebroker /bin/servicebroker

EXPOSE 8080
EXPOSE 8005
CMD ["/bin/servicebroker"]
