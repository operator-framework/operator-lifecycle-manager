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

# Tools #

# The tools required to build and test the project come from three sources:
# 1. .bingo/Variables.mk: tools that are orthogonal to OLM, e.g.
#   - golangci-lint
#   - helm
#   - kind
#   - setup-envtest
#   - yq
# 2. go.mod/tools.go: imports testing libraries, modules that need to be vendored and/or have a low tolerance for skew with the main module
#                     or because they are already imported by the main module but we want to pull down the whole dependency
#                     to run some cmd or script, or get some resource (e.g. CRD yaml files).
#   - OLM API CRDs
#   - Code generation tools, e.g. k8s.io/code-generator

# bingo manages the type 1 tools. If
#  a) we don't want their dependencies affecting ours, and
#  b) the tool's version doesn't need to track closely with OLM
# the tool goes here
include .bingo/Variables.mk

# go.mod/tools.go manages type 2 tools. If
# a) we use the library for development, e.g testing, assertion, etc.
# b) we need some resource (e.g. yaml file)
# c) need to run a script that is in the library
# c) need to track tools closely with OLM (e.g. have the same k8s library version)
# the tools belongs in go.mod. If your normal imports don't pull down everything you need into vendor
# then add the import to tools.go
# Note: The code generation tools are either being used in go:generate directives or
# they are setup in a different script, e.g. ./scripts/update_codegen.sh
TOOL_EXEC := go run -mod=vendor
GINKGO := $(TOOL_EXEC) github.com/onsi/ginkgo/v2/ginkgo

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
export VERSION_PATH := ${GIT_REPO}/pkg/version

ifeq ($(origin VERSION), undefined)
VERSION := $(shell git describe --tags --always --dirty)
endif
export VERSION

# GO_BUILD flags are set with = to allow for re-evaluation of the variables
export GO_BUILD_ASMFLAGS = all=-trimpath=$(PWD)
export GO_BUILD_GCFLAGS = all=-trimpath=$(PWD)
export GO_BUILD_FLAGS = -mod=vendor -buildvcs=false
export GO_BUILD_LDFLAGS = -s -w -X '$(VERSION_PATH).OLMVersion=$(VERSION)' -X '$(VERSION_PATH).GitCommit=$(GIT_COMMIT)' -extldflags "-static"
export GO_BUILD_TAGS = json1

# GO_TEST flags are set with = to allow for re-evaluation of the variables
# CGO_ENABLED=1 is required by the go test -race flag
GO_TEST_FLAGS = -race -count=1 $(if $(TEST),-run '$(TEST)',)
GO_TEST_ENV = CGO_ENABLED=1

# Test environment configuration #

# By default setup-envtest will write to $XDG_DATA_HOME, or $HOME/.local/share if that is not defined.
# If $HOME is not set, we need to specify a binary directory to prevent an error in setup-envtest.
# Useful for some CI/CD environments that set neither $XDG_DATA_HOME nor $HOME.
SETUP_ENVTEST_BIN_DIR_OVERRIDE=
ifeq ($(shell [[ $$HOME == "" || $$HOME == "/" ]] && [[ $$XDG_DATA_HOME == "" ]] && echo true ), true)
	SETUP_ENVTEST_BIN_DIR_OVERRIDE += --bin-dir /tmp/envtest-binaries
endif

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
e2e-build: local-build

.PHONY: local-build
local-build: IMAGE_TAG = local
local-build: image

.PHONY: run-local
run-local: local-build kind-create deploy

.PHONY: clean
clean: #HELP Clean up build artifacts
	@rm -rf cover.out
	@rm -rf bin

#SECTION Development

.PHONY: lint
lint: $(GOLANGCI_LINT) #HELP Run linters
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

.PHONY: bingo-upgrade
bingo-upgrade: $(BINGO) #EXHELP Upgrade tools
	@for pkg in $$($(BINGO) list | awk '{ print $$3 }' | tail -n +3 | sed 's/@.*//'); do \
		echo -e "Upgrading \033[35m$$pkg\033[0m to latest..."; \
		$(BINGO) get "$$pkg@latest"; \
	done

#SECTION Testing

# Note: We want to use TESTCMD = because we need it to be re-evaluated every time it is used
# since different targets might have different settings
UNIT_TEST_CMD = $(GO_TEST_ENV) go test $(GO_BUILD_FLAGS) -tags '$(GO_BUILD_TAGS)' $(GO_TEST_FLAGS)

.PHONE: test
test: clean unit test-split #HELP Run all tests

.PHONY: unit
unit: GO_TEST_ENV += KUBEBUILDER_ASSETS="$(KUBEBUILDER_ASSETS)"
unit: $(SETUP_ENVTEST) #HELP Run OLM unit tests with setup-envtest for kubernetes $(KUBE_MINOR).x
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
kind-clean: $(KIND) #HELP Delete kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0)
	$(KIND) delete cluster --name $(KIND_CLUSTER_NAME) || true

.PHONY: kind-create
kind-create: kind-clean #HELP Create a new kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0)
	$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image $(KIND_CLUSTER_IMAGE) $(KIND_CREATE_OPTS)
	$(KIND) export kubeconfig --name $(KIND_CLUSTER_NAME)

.PHONY: deploy
OLM_IMAGE := quay.io/operator-framework/olm:local
deploy: $(KIND) $(HELM) #HELP Deploy OLM to kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0) using $OLM_IMAGE (default: quay.io/operator-framework/olm:local)
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
undeploy: $(KIND) $(HELM) #HELP Uninstall OLM from kind cluster $KIND_CLUSTER_NAME (default: kind-olmv0)
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
GINKGO_OPTS += -v -randomize-suites -race -trace $(if $(E2E_FLAKE_ATTEMPTS),--flake-attempts='$(E2E_FLAKE_ATTEMPTS)') $(if $(E2E_SEED),-seed '$(E2E_SEED)') $(if $(TEST),-focus '$(TEST)',) $(if $(SKIP), -skip '$(SKIP)')

.PHONY: e2e
e2e: #HELP Run e2e tests against a cluster running OLM (params: $E2E_TEST_NS (operator), $E2E_INSTALL_NS (operator-lifecycle-manager), $E2E_CATALOG_NS (operator-lifecycle-manager), $E2E_TIMEOUT (90m), $E2E_FLAKE_ATTEMPTS (1), $TEST(undefined))
	$(GO_TEST_ENV) $(GINKGO) -timeout $(E2E_TIMEOUT) $(GINKGO_OPTS) $(E2E_GINKGO_OPTS) ./test/e2e -- -namespace=$(E2E_TEST_NS) -olmNamespace=$(E2E_INSTALL_NS) -catalogNamespace=$(E2E_CATALOG_NS) $(E2E_OPTS)

.PHONY: e2e-local
e2e-local: e2e-build kind-create deploy e2e

#SECTION Code Generation

.PHONY: gen-all #HELP Update OLM API, generate code and mocks
gen-all: manifests codegen update-k8s-values mockgen

.PHONY: update-k8s-values #HELP Update Helm Chart values with Kubernetes version
update-k8s-values:
	sed -i.bak -E 's/^( *enforceVersion:).*/\1 "v$(KUBE_MINOR)"/' deploy/chart/values.yaml
	sed -i.bak -E 's/^( *auditVersion:).*/\1 "v$(KUBE_MINOR)"/' deploy/chart/values.yaml
	sed -i.bak -E 's/^( *warnVersion:).*/\1 "v$(KUBE_MINOR)"/' deploy/chart/values.yaml
	rm deploy/chart/values.yaml.bak

.PHONY: manifests
manifests: vendor #HELP Copy OLM API CRD manifests to deploy/chart/crds
	./scripts/copy_crds.sh

.PHONY: codegen
codegen: #HELP Generate clients, deepcopy, listers, and informers
	./scripts/update_codegen.sh

.PHONY: mockgen
mockgen: #HELP Generate mocks
	# Generate mocks and silence the followign warning:
	# WARNING: Invoking counterfeiter multiple times from "go generate" is slow.
	# Consider using counterfeiter:generate directives to speed things up.
	# See https://github.com/maxbrunsfeld/counterfeiter#step-2b---add-counterfeitergenerate-directives for more information.
	# Set the "COUNTERFEITER_NO_GENERATE_WARNING" environment variable to suppress this message.
	COUNTERFEITER_NO_GENERATE_WARNING=1 go generate ./pkg/...

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

.PHONY: verify-update-k8s-values
verify-update-k8s-values: update-k8s-values #HELP Check if Helm Chart values are updated with k8s version
	$(MAKE) diff

.PHONY: verify
verify: vendor verify-codegen verify-mockgen verify-manifests verify-update-k8s-values #HELP Run all verification checks
	$(MAKE) diff

#SECTION Release

.PHONY: package
package: $(YQ) $(HELM) #HELP Package OLM for release
package:
ifndef TARGET
	$(error TARGET is undefined)
endif
ifndef RELEASE_VERSION
	$(error RELEASE_VERSION is undefined)
endif
	./scripts/package_release.sh $(RELEASE_VERSION) deploy/$(TARGET)/manifests/$(RELEASE_VERSION) deploy/$(TARGET)/values.yaml
	ln -sfFn ./$(RELEASE_VERSION) deploy/$(TARGET)/manifests/latest
ifeq ($(PACKAGE_QUICKSTART), true)
	./scripts/package_quickstart.sh deploy/$(TARGET)/manifests/$(RELEASE_VERSION) deploy/$(TARGET)/quickstart/olm.yaml deploy/$(TARGET)/quickstart/crds.yaml deploy/$(TARGET)/quickstart/install.sh
endif

.PHONY: release
release: manifests
	@echo "Generating the $(RELEASE_VERSION) release"
	$(MAKE) TARGET=upstream RELEASE_VERSION=$(RELEASE_VERSION) PACKAGE_QUICKSTART=true package

.PHONY: FORCE
FORCE:
