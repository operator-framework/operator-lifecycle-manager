SHELL := /bin/sh
ALM_PKG := github.com/coreos-inc/alm/cmd/alm
ALM_EXECUTABLE := ./bin/alm
CATALOG_PKG := github.com/coreos-inc/alm/cmd/catalog
CATALOG_EXECUTABLE := ./bin/catalog
IMAGE_REPO := quay.io/coreos/alm
IMAGE_TAG ?= "dev"
PKG_DIR := pkg

.PHONY: build test run clean vendor vendor-update coverage e2e

all: test build

COVERUTIL := $(GOPATH)/bin/gocoverutil

$(COVERUTIL):
	go get -u github.com/AlekSi/gocoverutil


test:
	go vet ./pkg/...
	go test -v ./Documentation/...
	go test -v -race ./pkg/...

test-cover: $(COVERUTIL)
	go vet ./pkg/...
	go test -v ./Documentation/...
	$(COVERUTIL) -coverprofile=cover.out test -v -race -covermode=atomic ./pkg/...
	go tool cover -func=cover.out

cover: $(COVERUTIL)
	$(COVERUTIL) -coverprofile=cover.out test -covermode=count ./pkg/...
	go tool cover -func=cover.out

coverage-html: cover
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
	./scripts/hack/k8s/codegen/update-generated.sh

update-catalog:
	./scripts/update-catalog.sh

counterfeiter := $(GOBIN)/counterfeiter
$(counterfeiter):
	go install github.com/maxbrunsfeld/counterfeiter

generate-mock-client: $(counterfeiter)
	go generate ./$(PKG_DIR)/...

make gen-all: gen-ci codegen generate-mock-client

# make ver=0.3.0 package
make release: update-catalog
	./scripts/package-release.sh $(ver) deploy/tectonic-alm-operator/manifests/$(ver) deploy/tectonic-alm-operator/values.yaml
