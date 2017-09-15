SHELL := /bin/sh
PKG := github.com/coreos-inc/alm/cmd/alm
PKGS := $(shell go list ./... | grep -v /vendor)
EXECUTABLE := ./bin/alm
.PHONY: test $(PKGS) run clean vendor vendor-update

all: test build

test: $(PKGS)

build:
	go build -o $(EXECUTABLE) $(PKG)

run: build
	./bin/$(EXECUTABLE)

$(PKGS):
	@gofmt -w=true $(GOPATH)/src/$@/*.go
	@go vet $@
	@go test -v -race $@

GLIDE := $(GOPATH)/bin/glide
$(GLIDE):
	go get github.com/Masterminds/glide

vendor: $(GLIDE)
	$(GLIDE) install -v

vendor-update: vendor
	$(GLIDE) up -v


clean:
	rm $(EXECUTABLE)
