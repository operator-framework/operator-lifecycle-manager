FROM golang:1.10 as builder
LABEL stage=builder
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
RUN curl -L https://github.com/stedolan/jq/releases/download/jq-1.5/jq-linux64 -o /bin/jq
RUN chmod +x /bin/jq
# copy just enough of the git repo to parse HEAD, used to record version in OLM binaries
COPY .git/HEAD .git/HEAD
COPY .git/refs/heads/. .git/refs/heads
RUN mkdir -p .git/objects
COPY Makefile Makefile
COPY OLM_VERSION OLM_VERSION
COPY pkg pkg
COPY vendor vendor
COPY cmd cmd
COPY test test
RUN make build-coverage
RUN go test -c -o /bin/e2e ./test/e2e/...

FROM alpine:latest as olm
LABEL stage=olm
WORKDIR /
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/olm /bin/olm
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/catalog /bin/catalog
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/package-server /bin/package-server
COPY --from=builder /go/src/github.com/operator-framework/operator-lifecycle-manager/bin/mock-ext-server /bin/mock-ext-server
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]

FROM alpine:latest
LABEL stage=e2e
RUN mkdir -p /var/e2e
WORKDIR /var/e2e
COPY --from=builder /bin/e2e /bin/e2e
COPY --from=builder /bin/jq /bin/jq
COPY ./test/e2e/e2e.sh /var/e2e/e2e.sh
COPY ./test/e2e/tap.jq /var/e2e/tap.jq
CMD ["/bin/e2e"]
