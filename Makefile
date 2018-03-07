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
	go test -v -race -coverprofile=cover.out -covermode=atomic -coverpkg ./pkg/... ./pkg/...

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
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(ALM_EXECUTABLE) $(ALM_PKG)

$(CATALOG_EXECUTABLE):
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(CATALOG_EXECUTABLE) $(CATALOG_PKG)

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(ALM_EXECUTABLE) $(ALM_PKG)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(CATALOG_EXECUTABLE) $(CATALOG_PKG)

# build versions of the binaries with coverage enabled
build-coverage:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -o $(ALM_EXECUTABLE) -c -covermode=count -coverpkg ./pkg/... $(ALM_PKG)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -o $(CATALOG_EXECUTABLE) -c -covermode=count -coverpkg ./pkg/... $(CATALOG_PKG)

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
	find . -iname "*.jsonnet" | xargs -L 1 jsonnet fmt -i -n 4
	find . -iname "*.libsonnet" | xargs -L 1 jsonnet fmt -i -n 4

gen-ci: fmt-ci
	ffctl gen

CODEGEN := ./vendor/k8s.io/code-generator/generate-groups.sh

$(CODEGEN):
	# dep doesn't currently support downloading dependencies that don't have go in the top-level dir.
	# can move to managing with dep when merged: https://github.com/golang/dep/pull/1545
	mkdir -p vendor/k8s.io/code-generator
	git clone --branch release-1.9 https://github.com/kubernetes/code-generator.git vendor/k8s.io/code-generator

codegen: $(CODEGEN)
	# codegen tools currently don't allow specifying custom boilerplate, so we move ours to the default location
	mkdir -p $(GOPATH)/src/k8s.io/kubernetes/hack/boilerplate
	cp boilerplate.go.txt $(GOPATH)/src/k8s.io/kubernetes/hack/boilerplate/boilerplate.go.txt
	$(CODEGEN) all github.com/coreos-inc/alm/pkg/client github.com/coreos-inc/alm/pkg/apis "catalogsource:v1alpha1 clusterserviceversion:v1alpha1 installplan:v1alpha1 subscription:v1alpha1 uicatalogentry:v1alpha1"
	# codegen doesn't respect pluralnames, so we manually set them here
	find ./pkg/client -type f -exec sed -i.bak 's/\"catalogsource/\"catalogsource-v1/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"catalogsources/\"catalogsource-v1s/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"clusterserviceversion/\"clusterserviceversion-v1/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"clusterserviceversions/\"clusterserviceversion-v1s/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"installplan/\"installplan-v1/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"installplans/\"installplan-v1s/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"subscription/\"subscription-v1/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"subscriptions/\"subscription-v1s/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"uicatalogentry/\"uicatalogentry-v1/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/\"uicatalogentries/\"uicatalogentry-v1s/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/Group: \"catalogsource-v1\"/Group: \"app.coreos.com"/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/Group: \"clusterserviceversion-v1\"/Group: \"app.coreos.com"/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/Group: \"installplan-v1\"/Group: \"app.coreos.com"/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/Group: \"subscription-v1\"/Group: \"app.coreos.com"/g' {} \; -exec rm {}.bak \;
	find ./pkg/client -type f -exec sed -i.bak 's/Group: \"uicatalogentry-v1\"/Group: \"app.coreos.com"/g' {} \; -exec rm {}.bak \;

verify-codegen: codegen
	git diff --exit-code

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

# make ver=0.3.0 release
make release: update-catalog
	./scripts/package-release.sh $(ver) deploy/tectonic-alm-operator/manifests/$(ver) deploy/tectonic-alm-operator/values.yaml
