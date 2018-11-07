FROM golang:1.10
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY Makefile Makefile
COPY pkg pkg
COPY vendor vendor
RUN make codegen

