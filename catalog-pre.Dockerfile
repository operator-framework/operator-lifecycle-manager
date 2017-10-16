FROM alpine:latest
RUN apk --no-cache add ca-certificates

COPY bin/catalog /bin/

EXPOSE 8080
CMD ["/bin/catalog"]
