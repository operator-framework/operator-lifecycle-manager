FROM golang:1.10
WORKDIR /go/src/github.com/operator-framework/operator-lifecycle-manager
COPY Makefile Makefile
COPY pkg pkg
COPY vendor vendor
COPY scripts/generate_groups.sh vendor/k8s.io/code-generator/generate_groups.sh
COPY boilerplate.go.txt boilerplate.go.txt
COPY boilerplate.go.txt vendor/k8s.io/code-generator/hack/boilerplate.go.txt
RUN make codegen

