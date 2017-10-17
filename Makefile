SHELL := /bin/sh
ALM_PKG := github.com/coreos-inc/alm/cmd/alm
ALM_EXECUTABLE := ./bin/alm
CATALOG_PKG := github.com/coreos-inc/alm/cmd/catalog
CATALOG_EXECUTABLE := ./bin/catalog
IMAGE_REPO := quay.io/coreos/alm

.PHONY: test run clean vendor vendor-update

all: test build

test:
	go vet `glide novendor`
	go test -v -race `glide novendor`

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -i -o $(ALM_EXECUTABLE) $(ALM_PKG)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -i -o $(CATALOG_EXECUTABLE) $(CATALOG_PKG)

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

codegen:
	hack/k8s/codegen/update-generated.sh

generate-mock-client:
	@mockgen -package=client -source=client/clusterserviceversion_client.go > client/mock_client.go
	@mockgen -package=install -source=install/resolver.go > install/mock_resolver.go

make gen-all: gen-ci codegen generate-mock-client
