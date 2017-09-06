FROM golang:1.9-alpine
WORKDIR /go/src/github.com/coreos-inc/alm
RUN apk add --no-cache make
COPY . .
RUN make build

# TODO(jzelinskie): remove when multistep build is available
EXPOSE 8080
CMD ["./bin/alm"]


# TODO(jzelinskie): uncomment when multi-step build is available
#FROM alpine:latest
#EXPOSE 8080
#CMD ["./alm"]
#RUN apk --no-cache add ca-certificates
#COPY --from=0 /go/src/github.com/coreos-inc/alm/bin .
