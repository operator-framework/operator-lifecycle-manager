ARG BASE_TAG=master
FROM quay.io/coreos/alm-ci:${BASE_TAG}

# Cache Dep first
COPY glide.yaml glide.lock Makefile ./
RUN make vendor

# Build bin
COPY . .
RUN make build && cp bin/alm /bin/alm

EXPOSE 8080
CMD ["/bin/alm"]
