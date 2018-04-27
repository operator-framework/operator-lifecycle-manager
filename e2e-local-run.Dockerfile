FROM golang:1.10 as builder
WORKDIR /go/src/github.com/coreos/alm
RUN apt-get update
RUN apt-get install -y jq
COPY pkg pkg
COPY vendor vendor
COPY e2e e2e
RUN go test -c -o /bin/e2e ./test/e2e/...
CMD ["./test/e2e/e2e.sh"]
