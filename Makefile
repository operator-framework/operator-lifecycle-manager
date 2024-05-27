#####################################################
#  Operator-Framework - Operator Lifecycle Manager  #
#####################################################

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL := /usr/bin/env bash -o pipefail
.SHELLFLAGS := -ec

# Undefine GOFLAGS environment variable.
ifdef GOFLAGS
$(warning Undefining GOFLAGS set in CI)
undefine GOFLAGS
endif

# Target environment and Dependencies #

# Minor Kubernetes version to build against derived from the client-go dependency version
KUBE_MINOR ?= $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1/')

# operator registry version to build against
OPERATOR_REGISTRY_VERSION ?= $(shell go list -m github.com/operator-framework/operator-registry | cut -d" " -f2 | sed 's/^v//')

# Pin operator registry images to the same version as the operator registry
export OPERATOR_REGISTRY_TAG ?= v$(OPERATOR_REGISTRY_VERSION)
export OPERATOR_REGISTRY_IMAGE ?= quay.io/operator-framework/opm:$(OPERATOR_REGISTRY_TAG)
export CONFIGMAP_SERVER_IMAGE ?= quay.io/operator-framework/configmap-operator-registry:$(OPERATOR_REGISTRY_TAG)

# Artifact settings #

PKG := github.com/operator-framework/operator-lifecycle-manager
IMAGE_REPO ?= quay.io/operator-framework/olm
IMAGE_TAG ?= "dev"

# Go build settings #

export CGO_ENABLED ?= 0
export GO111MODULE ?= on
export GIT_REPO := $(shell go list -m)
export GIT_COMMIT := $(shell git rev-parse HEAD)
export VERSION := $(shell cat OLM_VERSION)
export VERSION_PATH := ${GIT_REPO}/pkg/version

# GO_BUILD flags are set with = to allow for re-evaluation of the variables
export GO_BUILD_ASMFLAGS = all=-trimpath=$(PWD)
export GO_BUILD_GCFLAGS = all=-trimpath=$(PWD)
export GO_BUILD_FLAGS = -mod=vendor -buildvcs=false
export GO_BUILD_LDFLAGS = -s -w -X '$(VERSION_PATH).version=$(VERSION)' -X '$(VERSION_PATH).gitCommit=$(GIT_COMMIT)' -extldflags "-static"
export GO_BUILD_TAGS = json1

# GO_TEST flags are set with = to allow for re-evaluation of the variables
# CGO_ENABLED=1 is required by the go test -race flag
GO_TEST_FLAGS = -race -count=1 $(if $(TEST),-run '$(TEST)',)
GO_TEST_ENV = CGO_ENABLED=1

# Tools #
GO := GO111MODULE=on GOFLAGS="$(GO_BUILD_FLAGS)" go
YQ := $(GO) run github.com/mikefarah/yq/v3/
HELM := $(GO) run helm.sh/helm/v3/cmd/helm
KIND := $(GO) run sigs.k8s.io/kind
GINKGO := $(GO) run github.com/onsi/ginkgo/v2/ginkgo
SETUP_ENVTEST := $(GO) run sigs.k8s.io/controller-runtime/tools/setup-envtest
GOLANGCI_LINT := $(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint

# Test environment configuration #

# Unit test against the latest available version for the minor version of kubernetes we are building against e.g. 1.30.x
ENVTEST_KUBE_VERSION ?= $(KUBE_MINOR).x
KUBEBUILDER_ASSETS ?= $(shell $(SETUP_ENVTEST) use -p path $(KUBE_MINOR).x)

# Kind node image tags are in the format x.y.z we pin to version x.y.0 because patch releases and node images
# are not guaranteed to be available when a new version of the kube apis is released
KIND_CLUSTER_IMAGE := kindest/node:v$(KUBE_MINOR).0
KIND_CLUSTER_NAME ?= kind-olmv0

# Targets #
# Disable -j flag for make
.NOTPARALLEL:

.DEFAULT_GOAL := build

#SECTION General

.PHONY: all
all: test image #HELP Unit test, and build operator image

.PHONY: help
help: #HELP Display this help message
	@awk 'BEGIN {FS = ":.*#(EX)?HELP"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*#(EX)?HELP / { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^#SECTION / { printf "\n\033[1m%s\033[0m\n", substr($$0, 10) } ' $(MAKEFILE_LIST)

#SECTION Build

# Note: We want to use BUILDCMD = because we need it to be re-evaluated every time it is used
# since different targets might have different go build flags
BUILD_CMD = go build $(GO_BUILD_FLAGS) -ldflags '$(GO_BUILD_LDFLAGS)' -tags '$(GO_BUILD_TAGS)' -gcflags '$(GO_BUILD_GCFLAGS)' -asmflags '$(GO_BUILD_ASMFLAGS)'

CMDS := $(shell go list $(GO_BUILD_FLAGS) ./cmd/...)
$(CMDS): FORCE
	@echo "Building $(@)"
	$(BUILD_CMD) -o ./bin/$(shell basename $@) ./cmd/$(notdir $@)

.PHONY: build-utils
build-utils: #HELP Build utility binaries for local OS/ARCH
	$(BUILD_CMD) -o ./bin/cpb ./util/cpb

.PHONY: build #HELP Build binaries for local OS/ARCH
build: build-utils $(CMDS)

.PHONY: image
# Set GOOS to linux to build a linux binary for the image
# Don't set GOARCH because we want the default host architecture - this is important for developers on MacOS
image: export GOOS = linux
image: clean build #HELP Build image image for linux on host architecture
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) -f Dockerfile bin

.PHONY: e2e-build
# the e2e and experimental_metrics tags are required to get e2e tests to pass
# search the code for go:build e2e or go:build experimental_metrics to see where these tags are used
e2e-build: export GO_BUILD_TAGS += e2e experimental_metrics #HELP Build image for e2e testing
e2e-build: IMAGE_TAG = local
e2e-build: image

.PHONY: clean
clean: #HELP Clean up build artifacts
	@rm -rf cover.out
	@rm -rf bin

#SECTION Development

.PHONY: lint
lint: #HELP Run linters
	$(GOLANGCI_LINT) run $(GOLANGCI_LINT_ARGS)

.PHONY: vet
vet: #HELP Run go vet
	go vet $(GO_BUILD_FLAGS) ./...

.PHONY: fmt
fmt: #HELP Run go fmt
	go fmt ./...

vendor: #HELP Update vendored dependencies
	go mod tidy
	go mod vendor

#SECTION Testing

# Note: We want to use TESTCMD = because we need it to be re-evaluated every time it is used
# since different targets might have different settings
UNIT_TEST_CMD = $(GO_TEST_ENV) go test $(GO_BUILD_FLAGS) -tags '$(GO_BUILD_TAGS)' $(GO_TEST_FLAGS)

.PHONE: test
test: clean unit test-split #HELP Run all tests

.PHONY: unit
unit: GO_TEST_ENV += KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) #HELP Run OLM unit tests with setup-envtest for kubernetes $(KUBE_MINOR).x
unit:
	@echo "Running unit tests with setup_envtest for kubernetes $(KUBE_MINOR).x"
	# Test the olm and package server manager
	$(UNIT_TEST_CMD) ./pkg/controller/... ./pkg/...

.PHONY: test-split
test-split: #HELP Run e2e test split utility unit tests
	# Test the e2e test split utility
	$(UNIT_TEST_CMD) ./test/e2e/split/...

.PHONY: coverage
coverage: GO_TEST_FLAGS += -coverprofile=cover.out -covermode=atomic -coverpkg
coverage: unit #HELP Run OLM unit tests with coverage
	go tool cover -func=cover.out

#SECTION Deployment

.PHONY: kind-clean
kind-clean: #HELP Delete kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0)
	$(KIND) delete cluster --name $(KIND_CLUSTER_NAME) || true

.PHONY: kind-create
kind-create: kind-clean #HELP Create a new kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0)
	$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image $(KIND_CLUSTER_IMAGE) $(KIND_CREATE_OPTS)
	$(KIND) export kubeconfig --name $(KIND_CLUSTER_NAME)

.PHONY: deploy
OLM_IMAGE := quay.io/operator-framework/olm:local
deploy: #HELP Deploy OLM to kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0) using $OLM_IMAGE (default: quay.io/operator-framework/olm:local)
	$(KIND) load docker-image $(OLM_IMAGE) --name $(KIND_CLUSTER_NAME); \
	$(HELM) upgrade --install olm deploy/chart \
		--set debug=true \
		--set olm.image.ref=$(OLM_IMAGE) \
		--set olm.image.pullPolicy=IfNotPresent \
		--set catalog.image.ref=$(OLM_IMAGE) \
		--set catalog.image.pullPolicy=IfNotPresent \
		--set catalog.commandArgs=--configmapServerImage=$(CONFIGMAP_SERVER_IMAGE) \
		--set catalog.opmImageArgs=--opmImage=$(OPERATOR_REGISTRY_IMAGE) \
		--set package.image.ref=$(OLM_IMAGE) \
		--set package.image.pullPolicy=IfNotPresent \
		$(HELM_INSTALL_OPTS) \
		--wait;

.PHONY: undeploy
undeploy: #HELP Uninstall OLM from kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0)
	$(KIND) export kubeconfig --name $(KIND_CLUSTER_NAME)

	# Uninstall Helm chart and remove CRDs
	kubectl delete --all-namespaces --all sub
	kubectl delete --all-namespaces --all ip
	kubectl delete --all-namespaces --all csv
	kubectl delete --all-namespaces --all catsrc
	$(HELM) uninstall olm
	kubectl delete -f deploy/chart/crds

#SECTION e2e

# E2E test configuration
# Can be overridden when running make e2e, e.g. E2E_TIMEOUT=60m make e2e/e2e-local
E2E_TIMEOUT ?= 90m
E2E_TEST_NS ?= operators
E2E_INSTALL_NS ?= operator-lifecycle-manager
E2E_CATALOG_NS ?= $(E2E_INSTALL_NS)
E2E_FLAKE_ATTEMPTS ?= 1
GINKGO_OPTS += -v -randomize-suites -race -trace --show-node-events --flake-attempts=$(E2E_FLAKE_ATTEMPTS) $(if $(TEST),-focus '$(TEST)',)

.PHONY: e2e
e2e: #HELP Run e2e tests against a cluster running OLM (params: $E2E_TEST_NS (operator), $E2E_INSTALL_NS (operator-lifecycle-manager), $E2E_CATALOG_NS (operator-lifecycle-manager), $E2E_TIMEOUT (90m), $E2E_FLAKE_ATTEMPTS (1), $TEST(undefined))
	$(GO_TEST_ENV) $(GINKGO) -timeout $(E2E_TIMEOUT) $(GINKGO_OPTS) ./test/e2e -- -namespace=$(E2E_TEST_NS) -olmNamespace=$(E2E_INSTALL_NS) -catalogNamespace=$(E2E_CATALOG_NS) $(E2E_OPTS)

.PHONY: e2e-local
e2e-local: e2e-build kind-create deploy e2e

#SECTION Code Generation

.PHONY: gen-all #HELP Update OLM API, generate code and mocks
gen-all: manifests codegen mockgen

.PHONY: manifests
manifests: vendor #HELP Copy OLM API CRD manifests to deploy/chart/crds
	./scripts/copy_crds.sh

.PHONY: codegen
codegen: #HELP Generate clients, deepcopy, listers, and informers
	./scripts/update_codegen.sh

.PHONY: mockgen
mockgen: #HELP Generate mocks
	./scripts/update_mockgen.sh

#SECTION Verification

.PHONY: diff
diff:
	git diff --exit-code

.PHONY: verify-codegen
verify-codegen: codegen #HELP Check client, deepcopy, listers, and informers are up to date
	$(MAKE) diff

.PHONY: verify-mockgen
verify-mockgen: mockgen #HELP Check mocks are up to date
	$(MAKE) diff

.PHONY: verify-manifests
verify-manifests: manifests #HELP Check CRD manifests are up to date
	$(MAKE) diff

.PHONY: verify
verify: vendor verify-codegen verify-mockgen verify-manifests #HELP Run all verification checks
	$(MAKE) diff

#SECTION Release

.PHONY: pull-opm
pull-opm:
	docker pull $(OPERATOR_REGISTRY_IMAGE)

.PHONY: package
package: OLM_RELEASE_IMG_REF=$(shell docker inspect --format='{{index .RepoDigests 0}}' $(IMAGE_REPO):$(RELEASE_VERSION))
package: OPM_IMAGE_REF=$(shell docker inspect --format='{{index .RepoDigests 0}}' $(OPERATOR_REGISTRY_IMAGE))
package:
ifndef TARGET
	$(error TARGET is undefined)
endif
ifndef RELEASE_VERSION
	$(error RELEASE_VERSION is undefined)
endif
	@echo "Getting operator registry image"
	docker pull $(OPERATOR_REGISTRY_IMAGE)
	$(YQ) w -i deploy/$(TARGET)/values.yaml olm.image.ref $(OLM_RELEASE_IMG_REF)
	$(YQ) w -i deploy/$(TARGET)/values.yaml catalog.image.ref $(OLM_RELEASE_IMG_REF)
	$(YQ) w -i deploy/$(TARGET)/values.yaml package.image.ref $(OLM_RELEASE_IMG_REF)
	$(YQ) w -i deploy/$(TARGET)/values.yaml -- catalog.opmImageArgs "--opmImage=$(OPM_IMAGE_REF)"
	./scripts/package_release.sh $(RELEASE_VERSION) deploy/$(TARGET)/manifests/$(RELEASE_VERSION) deploy/$(TARGET)/values.yaml
	ln -sfFn ./$(RELEASE_VERSION) deploy/$(TARGET)/manifests/latest
ifeq ($(PACKAGE_QUICKSTART), true)
	./scripts/package_quickstart.sh deploy/$(TARGET)/manifests/$(RELEASE_VERSION) deploy/$(TARGET)/quickstart/olm.yaml deploy/$(TARGET)/quickstart/crds.yaml deploy/$(TARGET)/quickstart/install.sh
endif

.PHONY: release
release: RELEASE_VERSION=v$(shell cat OLM_VERSION) #HELP Generate an OLM release (NOTE: before running release, bump the version in ./OLM_VERSION and push to master, then tag those builds in quay with the version in ./OLM_VERSION)
release: pull-opm manifests # pull the opm image to get the digest
	@echo "Generating the $(RELEASE_VERSION) release"
	docker pull $(IMAGE_REPO):$(RELEASE_VERSION)
	$(MAKE) TARGET=upstream RELEASE_VERSION=$(RELEASE_VERSION) PACKAGE_QUICKSTART=true package

.PHONY: FORCE
FORCE:
