FROM alpine:latest
RUN apk --no-cache add ca-certificates

COPY bin/alm /bin/

EXPOSE 8080
CMD ["/bin/alm"]