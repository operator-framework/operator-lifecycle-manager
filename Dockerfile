FROM quay.io/coreos/alm-ci:base as builder
LABEL builder=true
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
RUN curl -L https://github.com/stedolan/jq/releases/download/jq-1.5/jq-linux64 -o /bin/jq
# Cache Dep first
COPY Gopkg.toml Gopkg.lock Makefile ./
RUN make vendor
COPY . .
RUN make build
RUN go test -c -o /bin/e2e ./test/e2e/...

FROM alpine:latest as olm
LABEL olm=true
WORKDIR /
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/alm /bin/alm
EXPOSE 8080
CMD ["/bin/alm"]

FROM alpine:latest as catalog
LABEL catalog=true
WORKDIR /
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/catalog /bin/catalog
EXPOSE 8080
CMD ["/bin/catalog"]

FROM alpine:latest as broker
LABEL broker=true
WORKDIR /
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/servicebroker /bin/servicebroker
EXPOSE 8080
EXPOSE 8005
CMD ["/bin/servicebroker"]

FROM quay.io/coreos/alm-ci:base
LABEL e2e=true
COPY --from=builder /bin/e2e /bin/e2e
COPY --from=builder /bin/jq /bin/jq
CMD ["/bin/e2e"]
