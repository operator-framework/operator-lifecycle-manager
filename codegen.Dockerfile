FROM golang:1.10
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY Makefile Makefile
COPY pkg pkg
COPY vendor vendor
COPY scripts/generate_groups.sh scripts/generate_groups.sh
COPY boilerplate.go.txt boilerplate.go.txt
RUN make codegen

