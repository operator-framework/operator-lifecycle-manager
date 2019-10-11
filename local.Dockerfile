FROM alpine:latest
WORKDIR /
COPY config/crd/bases /config/crd/bases
COPY bin/olm /bin/olm
COPY bin/catalog /bin/catalog
COPY bin/package-server /bin/package-server
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]
