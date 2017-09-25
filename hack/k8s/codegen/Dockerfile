FROM golang:1.9-alpine

RUN apk add --update --no-cache git bash \
  && go get k8s.io/code-generator || true \
  && cd src/k8s.io/code-generator \
  && go install ./cmd/...
