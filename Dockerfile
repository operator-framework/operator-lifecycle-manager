# Note: This dockerfile does not build the binaries
# required and is intended to be built only with the
# 'make build' or 'make release' targets.

# use debug tag to keep a shell around for backwards compatibility with the previous alpine image
FROM gcr.io/distroless/static:debug
WORKDIR /
# bundle unpack Jobs require cp at /bin/cp
RUN ["/busybox/ln", "-s", "/busybox/cp", "/bin/cp"]
COPY olm bin/olm
COPY catalog bin/catalog
COPY package-server bin/package-server
COPY copy-content bin/copy-content
COPY cpb /bin/cpb

EXPOSE 8080
EXPOSE 5443

USER 65532:65532

ENTRYPOINT ["/bin/olm"]
