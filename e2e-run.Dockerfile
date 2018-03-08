FROM quay.io/coreos/alm-ci:base
WORKDIR /go/src/github.com/coreos-inc/alm
RUN apt-get update && apt-get install -y jq
COPY . .
RUN make vendor
RUN go test -c -o /bin/e2e ./e2e/...
CMD ["/bin/e2e"]
