FROM golang:1.10beta1 as builder
WORKDIR /go/src/github.com/coreos-inc/alm
RUN apt-get update && apt-get install -y jq
COPY pkg pkg
COPY vendor vendor
COPY e2e e2e
RUN go test -c -o /bin/e2e ./e2e/...
CMD ["./e2e/e2e.sh"]