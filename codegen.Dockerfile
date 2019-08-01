FROM golang:1.12
WORKDIR $GOPATH/src/github.com/operator-framework/operator-lifecycle-manager
COPY Makefile Makefile
COPY pkg pkg
COPY vendor vendor
COPY scripts/generate_internal_groups.sh scripts/generate_internal_groups.sh
COPY boilerplate.go.txt boilerplate.go.txt
RUN make codegen