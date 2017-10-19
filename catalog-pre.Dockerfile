FROM alpine:latest
RUN apk --no-cache add ca-certificates

COPY bin/catalog /bin/
COPY catalog_resources /var/catalog_resources

EXPOSE 8080
CMD ["/bin/catalog"]
