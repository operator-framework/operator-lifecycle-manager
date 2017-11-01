SHELL := /bin/sh
ALM_PKG := github.com/coreos-inc/alm/cmd/alm
ALM_EXECUTABLE := ./bin/alm
CATALOG_PKG := github.com/coreos-inc/alm/cmd/catalog
CATALOG_EXECUTABLE := ./bin/catalog
IMAGE_REPO := quay.io/coreos/alm
IMAGE_TAG ?= "dev"
TOP_LEVEL := $(dir $(shell glide novendor))
PACKAGES := $(shell find $(TOP_LEVEL) -type d -not -path '*/\.*')

.PHONY: test run clean vendor vendor-update coverage

all: test build

test:
	go vet `glide novendor`
	go test -v -race `glide novendor`

coverage:
	echo "mode: count" > coverage-all.out
	$(foreach pkg,$(PACKAGES),\
		go test -coverprofile=coverage.out -covermode=count $(pkg);\
		tail -n +2 coverage.out >> coverage-all.out;)
	go tool cover -html=coverage-all.out

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
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

clean:
	rm $(ALM_EXECUTABLE)
	rm $(CATALOG_EXECUTABLE)

fmt-ci:
	find . -iname "*.jsonnet" | xargs jsonnet fmt -i -n 4
	find . -iname "*.libsonnet" | xargs jsonnet fmt -i -n 4

gen-ci: fmt-ci
	ffctl gen

codegen:
	hack/k8s/codegen/update-generated.sh

MOCKGEN := $(GOBIN)/mockgen
$(MOCKGEN):
	go get github.com/golang/mock/mockgen

generate-mock-client: $(MOCKGEN)
	mockgen -package=client -source=client/clusterserviceversion_client.go > client/zz_generated.mock_clusterserviceversion_client.go
	mockgen -package=client -source=client/installplan_client.go > client/zz_generated.mock_installplan_client.go
	mockgen -package=client -source=client/alphacatalogentry_client.go > client/zz_generated.mock_alphacatalogentry_client.go
	mockgen -package=client -source=client/deployment_install_client.go > client/zz_generated.mock_deployment_install_client.go
	mockgen -package=install -source=install/resolver.go > install/zz_generated.mock_resolver.go

make gen-all: gen-ci codegen generate-mock-client
