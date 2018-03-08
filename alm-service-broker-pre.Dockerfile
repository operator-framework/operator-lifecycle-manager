FROM alpine:latest
RUN apk --no-cache add ca-certificates

COPY bin/servicebroker /bin/
COPY catalog_resources /var/catalog_resources

EXPOSE 8080
EXPOSE 8005
CMD ["/bin/servicebroker"]
