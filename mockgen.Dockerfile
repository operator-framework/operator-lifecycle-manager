FROM golang:1.12
WORKDIR /operator-lifecycle-manager
COPY Makefile Makefile
COPY cmd cmd
COPY pkg pkg
COPY vendor vendor
COPY go.mod go.mod
COPY go.sum go.sum
COPY scripts/generate_mocks.sh scripts/generate_mocks.sh
COPY boilerplate.go.txt boilerplate.go.txt
RUN chmod +x scripts/generate_mocks.sh && \
    make mockgen