##########################
#  OLM - Build and Test  #
##########################

SHELL := /bin/bash
PKG   := github.com/operator-framework/operator-lifecycle-manager
CMDS  := $(addprefix bin/, $(shell go list ./cmd/... | xargs -l basename))
IMAGE_REPO := quay.io/coreos/olm
IMAGE_TAG ?= "dev"

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e

all: test build

test: schema-check cover.out

schema-check:
	go test -v ./test/schema

cover.out: schema-check
	go test -v -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out
	go tool cover -func=cover.out

coverage-html: cover.out
	go tool cover -html=cover.out

build: $(CMDS)

$(CMDS):
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -o $@ $(PKG)/cmd/$(shell basename $@)

CATALOG_CHART:=deploy/chart/templates/08-tectonicocs.configmap.yaml
CATALOG_RELEASE:=catalog_resources/ocs/tectonicocs.configmap.yaml

$(CATALOG_CHART) $(CATALOG_RELEASE): catalog_resources/ocs/*.crd.yaml \
	catalog_resources/ocs/*.clusterserviceversion.yaml \
	catalog_resources/ocs/*.package.yaml
	. ./scripts/build_catalog_configmap.sh catalog_resources/ocs 'tectonic-ocs' $@

build/chart/values.yaml: deploy/chart/values.yaml
	mkdir -p build/chart
	cp deploy/chart/values.yaml build/chart/values.yaml

build/chart/Chart.yaml: deploy/chart/Chart.yaml
	mkdir -p build/chart
	cp deploy/chart/Chart.yaml build/chart/Chart.yaml
	echo "version: ver=1.0.0-local" >> build/chart/Chart.yaml

RESOURCES:=$(shell ls deploy/chart/templates/*yaml | xargs -l basename)
CHARTS:=$(addprefix build/chart/templates/,$(RESOURCES))
MANIFESTS:=$(addprefix build/resources/,$(RESOURCES))
build/chart/templates/%.yaml: deploy/chart/templates/%.yaml
	mkdir -p build/chart/templates
	cp $< $@

$(MANIFESTS): $(CHARTS) build/chart/Chart.yaml build/chart/values.yaml \
	Documentation/install/local-values.yaml
	mkdir -p build/resources
	helm template -n olm -f Documentation/install/local-values.yaml \
		-x templates/$(shell basename $@) build/chart > $@

rc: $(CATALOG_CHART) $(MANIFESTS)

run-local: release
	. ./scripts/build_local.sh
	. ./scripts/install_local.sh local build/resources

run-local-shift: rc
	sed -i 's/rbac.authorization.k8s.io/authorization.openshift.io/' build/resources/02-alm-operator.rolebinding.yaml
	. ./scripts/build_local_shift.sh
	. ./scripts/install_local.sh local build/resources

e2e-local: rc
	./scripts/build_local.sh
	./scripts/run_e2e_local.sh

e2e-local-shift: rc
	./scripts/build_local_shift.sh
	./scripts/run_e2e_local.sh

e2e-local-docker: rc
	./scripts/build_local.sh
	./scripts/run_e2e_docker.sh

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
	rm -rf bin
	rm -rf test/e2e/test-resources
	rm -rf test/e2e/log
	rm -rf build

CI := $(shell find . -iname "*.jsonnet") $(shell find . -iname "*.libsonnet")
$(CI):
	jsonnet fmt -i -n 4 $@

.gitlab-ci.yml: $(CI)
	ffctl gen

CODEGEN := ./vendor/k8s.io/code-generator/generate-groups.sh

$(CODEGEN):
	# dep doesn't currently support downloading dependencies that don't have go in the top-level dir.
	# can move to managing with dep when merged: https://github.com/golang/dep/pull/1545
	mkdir -p vendor/k8s.io/code-generator
	git clone --branch release-1.9 https://github.com/kubernetes/code-generator.git vendor/k8s.io/code-generator
	# codegen tools currently don't allow specifying custom boilerplate, so we move ours to the default location
	mkdir -p $(GOPATH)/src/k8s.io/kubernetes/hack/boilerplate
	cp boilerplate.go.txt $(GOPATH)/src/k8s.io/kubernetes/hack/boilerplate/boilerplate.go.txt

define replace
@find ./pkg/api/client -type f -exec \
		sed -i.bak 's/\(\"'$(1)'\)\(-v1\)*\(s\)*/\1-v1\3/g' {} \; -exec rm {}.bak \;
@find ./pkg/api/client -type f -exec \
		sed -i.bak 's/Group: \"'$(1)'-v1\"/Group: \"app.coreos.com\"/g' {} \; -exec rm {}.bak \;
endef
codegen: $(CODEGEN)
	$(CODEGEN) all $(PKG)/pkg/api/client $(PKG)/pkg/api/apis \
		"catalogsource:v1alpha1 clusterserviceversion:v1alpha1 installplan:v1alpha1 subscription:v1alpha1"
	# codegen doesn't respect pluralnames, so we manually set them here
	$(call replace,"catalogsource")
	$(call replace,"clusterserviceversion")
	$(call replace,"installplan")
	$(call replace,"subscription")

verify-codegen: codegen
	git diff --exit-code

verify-catalog: $(CATALOG_CHART)
	git diff --exit-code

counterfeiter := $(GOBIN)/counterfeiter
$(counterfeiter):
	go install github.com/maxbrunsfeld/counterfeiter

generate-mock-client: $(counterfeiter)
	go generate ./$(PKG_DIR)/...

make gen-all: gen-ci codegen generate-mock-client

# make ver=0.3.0 release
make release: $(CATALOG_RELEASE)
	mkdir -p build/tectonic-alm-operator/manifests/$(ver)
	./scripts/package-release.sh $(ver) build/tectonic-alm-operator/manifests/$(ver) deploy/tectonic-alm-operator/values.yaml
