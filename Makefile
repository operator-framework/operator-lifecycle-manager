SHELL := /bin/sh
PKG := github.com/coreos-inc/alm/cmd/alm
EXECUTABLE := ./bin/alm
IMAGE_REPO := quay.io/coreos/alm

.PHONY: test run clean vendor vendor-update

all: test build

test:
	go vet `glide novendor`
	go test -v -race `glide novendor`

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(EXECUTABLE) $(PKG)

run: build
	./bin/$(EXECUTABLE)

GLIDE := $(GOPATH)/bin/glide

$(GLIDE):
	go get github.com/Masterminds/glide

glide: $(GLIDE)

vendor: $(GLIDE)
	$(GLIDE) install -v

vendor-update: vendor
	$(GLIDE) up -v

container: build
	docker build -t $(IMAGE_REPO):dev .

clean:
	rm $(EXECUTABLE)

fmt-ci:
	find . -iname "*.jsonnet" | xargs jsonnet fmt -i -n 4
	find . -iname "*.libsonnet" | xargs jsonnet fmt -i -n 4

gen-ci: fmt-ci
	ffctl gen
