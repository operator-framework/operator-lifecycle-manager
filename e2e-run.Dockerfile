FROM quay.io/coreos/alm-ci:base
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
RUN apt-get update && apt-get install -y jq
COPY . .
RUN make vendor
RUN go test -c -o /bin/e2e ./test/e2e/...
CMD ["/bin/e2e"]
