FROM quay.io/fedora/fedora:34-x86_64 as builder
LABEL stage=builder
WORKDIR /build

# install dependencies and go 1.16

# copy just enough of the git repo to parse HEAD, used to record version in OLM binaries
RUN dnf update -y && dnf install -y bash make git mercurial jq wget golang && dnf upgrade -y
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
COPY util util
COPY test test
RUN CGO_ENABLED=0 make build
RUN make build-util

# use debug tag to keep a shell around for backwards compatibility with the previous alpine image
FROM gcr.io/distroless/static:debug
LABEL stage=olm
WORKDIR /
# bundle unpack Jobs require cp at /bin/cp
RUN ["/busybox/ln", "-s", "/busybox/cp", "/bin/cp"]
COPY --from=builder /build/bin/olm /bin/olm
COPY --from=builder /build/bin/catalog /bin/catalog
COPY --from=builder /build/bin/package-server /bin/package-server
COPY --from=builder /build/bin/cpb /bin/cpb
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]
