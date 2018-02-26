SHELL := /bin/sh
ALM_PKG := github.com/coreos-inc/alm/cmd/alm
ALM_EXECUTABLE := ./bin/alm
CATALOG_PKG := github.com/coreos-inc/alm/cmd/catalog
CATALOG_EXECUTABLE := ./bin/catalog
IMAGE_REPO := quay.io/coreos/alm
IMAGE_TAG ?= "dev"
PKG_DIR := pkg

.PHONY: build test test-docs run clean vendor vendor-update coverage e2e

all: test build

test-docs:
	go test -v ./Documentation/...

test: test-docs
	go test -v -race -coverprofile=cover.out -covermode=count -coverpkg ./pkg/... ./pkg/...

coverage: test
	go tool cover -func=cover.out

coverage-html: test
	go tool cover -html=cover.out

run-local: update-catalog
	. ./scripts/package-release.sh ver=1.0.0-local Documentation/install/resources Documentation/install/local-values.yaml
	. ./scripts/build_local.sh
	. ./scripts/install_local.sh local Documentation/install/resources
	rm -rf Documentation/install/resources

run-local-shift: update-catalog
	. ./scripts/package-release.sh ver=1.0.0-localshift Documentation/install/resources Documentation/install/local-values-shift.yaml
	. ./scripts/build_local_shift.sh
	. ./scripts/install_local.sh local Documentation/install/resources
	rm -rf Documentation/install/resources

e2e-local: update-catalog
	./scripts/build_local.sh
	./scripts/run_e2e_local.sh

e2e-local-docker: update-catalog
	./scripts/build_local.sh
	./scripts/run_e2e_docker.sh

$(ALM_EXECUTABLE):
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -i -o $(ALM_EXECUTABLE) $(ALM_PKG)

$(CATALOG_EXECUTABLE):
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -i -o $(CATALOG_EXECUTABLE) $(CATALOG_PKG)

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -i -o $(ALM_EXECUTABLE) $(ALM_PKG)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -i -o $(CATALOG_EXECUTABLE) $(CATALOG_PKG)

run: build
	./bin/$(EXECUTABLE)

DEP := $(GOPATH)/bin/dep

$(DEP):
	go get -u github.com/golang/dep/cmd/dep

vendor: $(DEP)
	$(DEP) ensure -v -vendor-only

vendor-update: $(DEP)
	$(DEP) ensure -v

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

CODEGEN := ./vendor/k8s.io/code-generator/generate-groups.sh

$(CODEGEN):
    # dep doesn't currently support downloading dependencies that don't have go in the top-level dir.
    # can move to managing with dep when merged: https://github.com/golang/dep/pull/1545
	mkdir -p vendor/k8s.io/code-generator
	git clone --branch release-1.9 git@github.com:kubernetes/code-generator.git vendor/k8s.io/code-generator

codegen: $(CODEGEN)
	$(CODEGEN) all github.com/coreos-inc/alm/pkg/client github.com/coreos-inc/alm/pkg/apis "catalogsource:v1alpha1 clusterserviceversion:v1alpha1 installplan:v1alpha1 subscription:v1alpha1 uicatalogentry:v1alpha1"

update-catalog:
	./scripts/update-catalog.sh

verify-catalog: update-catalog
	git diff --exit-code

counterfeiter := $(GOBIN)/counterfeiter
$(counterfeiter):
	go install github.com/maxbrunsfeld/counterfeiter

generate-mock-client: $(counterfeiter)
	go generate ./$(PKG_DIR)/...

make gen-all: gen-ci codegen generate-mock-client

# make ver=0.3.0 package
make release: update-catalog
	./scripts/package-release.sh $(ver) deploy/tectonic-alm-operator/manifests/$(ver) deploy/tectonic-alm-operator/values.yaml
