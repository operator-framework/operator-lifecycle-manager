SHELL := /bin/bash
PKG := github.com/coreos-inc/alm
PKGS := $(shell go list ./... | grep -v /vendor)
EXECUTABLE := ./bin/alm
.PHONY: test $(PKGS) run clean vendor vendor-update

all: test build

test: $(PKGS)

build:
	go build -o $(EXECUTABLE) $(PKG)

run: build
	./bin/$(EXECUTABLE)

$(PKGS): vendor
	gofmt -w=true $(GOPATH)/src/$@/*.go
	go vet $@
	go test -v -race $@

GODEP := $(GOPATH)/bin/dep
$(GODEP):
	go get github.com/golang/dep/cmd/dep

vendor: $(GODEP)
	$(GODEP) ensure

vendor-update: vendor $(GODEP)
	$(GODEP) ensure -update
	$(GODEP) prune

clean:
	rm $(EXECUTABLE)
