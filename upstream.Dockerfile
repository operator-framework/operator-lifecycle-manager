FROM quay.io/coreos/alm-ci:base as builder
LABEL stage=builder
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
RUN curl -L https://github.com/stedolan/jq/releases/download/jq-1.5/jq-linux64 -o /bin/jq
RUN chmod +x /bin/jq
# Cache Dep first
COPY . .
RUN make build
RUN go test -c -o /bin/e2e ./test/e2e/...

FROM alpine:latest as olm
LABEL stage=olm
WORKDIR /
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/olm /bin/olm
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/catalog /bin/catalog
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/package-server /bin/package-server
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]

FROM quay.io/coreos/alm-ci:base
LABEL stage=e2e
RUN mkdir -p /var/e2e
WORKDIR /var/e2e
COPY --from=builder /bin/e2e /bin/e2e
COPY --from=builder /bin/jq /bin/jq
COPY ./test/e2e/e2e.sh /var/e2e/e2e.sh
COPY ./test/e2e/tap.jq /var/e2e/tap.jq
CMD ["/bin/e2e"]
