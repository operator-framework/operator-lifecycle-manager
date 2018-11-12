FROM golang:1.10
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY Makefile Makefile
COPY pkg pkg
COPY vendor vendor
COPY scripts/generate_mocks.sh scripts/generate_mocks.sh
RUN chmod +x scripts/generate_mocks.sh
COPY boilerplate.go.txt boilerplate.go.txt
RUN make generate-mock-client