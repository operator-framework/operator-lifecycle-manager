FROM golang:1.12-alpine as builder
LABEL stage=builder
WORKDIR /build

RUN apk update && apk add bash make git mercurial jq && apk upgrade

# copy just enough of the git repo to parse HEAD, used to record version in OLM binaries
COPY .git/HEAD .git/HEAD
COPY .git/refs/heads/. .git/refs/heads
RUN mkdir -p .git/objects
COPY Makefile Makefile
COPY OLM_VERSION OLM_VERSION
COPY pkg pkg
COPY vendor vendor
COPY go.mod go.mod
COPY go.sum go.sum
COPY cmd cmd
COPY test test
RUN make build

FROM alpine:latest
LABEL stage=olm
WORKDIR /
COPY config/crd/bases /config/crd/bases
COPY --from=builder /build/bin/olm /bin/olm
COPY --from=builder /build/bin/catalog /bin/catalog
COPY --from=builder /build/bin/package-server /bin/package-server
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]
