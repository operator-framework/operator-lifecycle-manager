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

cover:
	go test -cover -tags test `glide novendor`

coverage-html:
	echo "mode: count" > coverage-all.out
	$(foreach pkg,$(PACKAGES),\
		go test -coverprofile=coverage.out -covermode=count -tags test $(pkg);\
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
	mockgen -source=client/clusterserviceversion_client.go -package=alm > operators/alm/zz_generated.mock_clusterserviceversion_client_test.go
	mockgen -package=client -source=client/installplan_client.go > client/zz_generated.mock_installplan_client_test.go
	mockgen -source=client/alphacatalogentry_client.go  -package=catalog > catalog/zz_generated.mock_alphacatalogentry_client_test.go
	mockgen -source=client/deployment_install_client.go -package=install > install/zz_generated.mock_deployment_install_client_test.go
	# can't use "source" mockgen, see: https://github.com/golang/mock/issues/11#issuecomment-294380653
	mockgen -package=alm -imports client=github.com/coreos-inc/alm/vendor/github.com/coreos-inc/operator-client/pkg/client,v1=github.com/coreos-inc/alm/vendor/k8s.io/apimachinery/pkg/apis/meta/v1 github.com/coreos-inc/alm/install StrategyResolverInterface > operators/alm/zz_generated.mock_resolver_test.go
	# mockgen doesn't handle vendored dependencies well: https://github.com/golang/mock/issues/30
	sed -i '' s,github.com/coreos-inc/alm/vendor/,, operators/alm/zz_generated.mock_resolver_test.go
	goimports -w operators/alm/zz_generated.mock_resolver_test.go

make gen-all: gen-ci codegen generate-mock-client
