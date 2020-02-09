FROM alpine:latest
WORKDIR /
COPY olm /bin/olm
COPY catalog /bin/catalog
COPY package-server /bin/package-server
COPY cpb /bin/cpb
EXPOSE 8080
EXPOSE 5443
CMD ["/bin/olm"]
