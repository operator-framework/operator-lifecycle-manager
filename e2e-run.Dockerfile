FROM quay.io/coreos/alm-ci:base
WORKDIR /go/src/github.com/coreos-inc/alm
RUN apt-get update && apt-get install -y jq
COPY . .
RUN make vendor-update
RUN go test -c -o /bin/e2e ./e2e/...
CMD ["./e2e/e2e.sh"]
