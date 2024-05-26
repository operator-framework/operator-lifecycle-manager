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

# Kind node image tags are in the format x.y.z we pin to version x.y.0 because patch releases and node images
# are not guaranteed to be available when a new version of the kube apis is released
KIND_CLUSTER_IMAGE := kindest/node:v$(KUBE_MINOR).0
KIND_CLUSTER_NAME ?= kind-olmv0

# Targets #
# Disable -j flag for make
.NOTPARALLEL:

.DEFAULT_GOAL := build

.PHONY: all
all: clean test container

#SECTION Build

# Note: We want to use BUILDCMD = because we need it to be re-evaluated every time it is used
# since different targets might have different go build flags
BUILDCMD = go build $(GO_BUILD_FLAGS) -ldflags '$(GO_BUILD_LDFLAGS)' -tags '$(GO_BUILD_TAGS)' -gcflags '$(GO_BUILD_GCFLAGS)' -asmflags '$(GO_BUILD_ASMFLAGS)'

CMDS := $(shell go list $(GO_BUILD_FLAGS) ./cmd/...)
$(CMDS): FORCE
	@echo "Building $(@)"
	$(BUILDCMD) -o ./bin/$(shell basename $@) ./cmd/$(notdir $@)

.PHONY: build-utils
build-utils:
	$(BUILDCMD) -o ./bin/cpb ./util/cpb

.PHONY: build
build: build-utils $(CMDS)

.PHONY: e2e-build
# the e2e and experimental_metrics tags are required to get e2e tests to pass
# search the code for go:build e2e or go:build experimental_metrics to see where these tags are used
e2e-build: export GO_BUILD_TAGS += e2e experimental_metrics
e2e-build: IMAGE_TAG = local
e2e-build: container

.PHONY: container
# Set GOOS to linux to build a linux binary for the container
# Don't set GOARCH because we want the default host architecture - this is important for developers on MacOS
container: export GOOS = linux
container: clean build
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) -f Dockerfile bin

.PHONY: clean
clean:
	@rm -rf cover.out
	@rm -rf bin

#SECTION Development

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run $(GOLANGCI_LINT_ARGS)

.PHONY: vet
vet:
	go vet $(GO_BUILD_FLAGS) ./...

.PHONY: fmt
fmt:
	go fmt ./...

vendor:
	go mod tidy
	go mod vendor

# SECTION Testing

KUBEBUILDER_ASSETS ?= $(shell $(SETUP_ENVTEST) use -p path $(KUBE_MINOR).x)
GO_TEST_FLAGS = -race -count=1 $(if $(TEST),-run '$(TEST)',)

# CGO_ENABLED=1 is required for -race to work
GO_TEST_ENV = CGO_ENABLED=1

.PHONE: test
test: clean unit test-split

.PHONY: unit
unit: GO_TEST_ENV += KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS)
unit:
	@echo "Running unit tests with setup_envtest for kubernetes $(KUBE_MINOR).x"
	# Test the olm and package server manager
	$(GO_TEST_ENV) go test $(GO_BUILD_FLAGS) -tags $(GO_BUILD_TAGS) $(GO_TEST_FLAGS) ./pkg/controller/... ./pkg/...

.PHONY: test-split
test-split:
	# Test the e2e test split utility
	$(GO_TEST_ENV) go test $(GO_BUILD_FLAGS) -tags $(GO_BUILD_TAGS) $(GO_TEST_FLAGS) ./test/e2e/split/...

.PHONY: coverage
coverage: GO_TEST_FLAGS += -coverprofile=cover.out -covermode=atomic -coverpkg
coverage: unit
	go tool cover -func=cover.out

#SECTION Deployment

.PHONY: kind-clean
kind-clean:
	$(KIND) delete cluster --name $(KIND_CLUSTER_NAME) || true

.PHONY: kind-create
kind-create: kind-clean
	$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image $(KIND_CLUSTER_IMAGE) $(KIND_CREATE_OPTS)
	$(KIND) export kubeconfig --name $(KIND_CLUSTER_NAME)

.PHONY: deploy
OLM_IMAGE := quay.io/operator-framework/olm:local
deploy:
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
undeploy:
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
GINKGO_OPTS += -v -randomize-suites -race -trace --show-node-events --flake-attempts=$(E2E_FLAKE_ATTEMPTS)

.PHONY: e2e
e2e:
	$(GO_TEST_ENV) $(GINKGO) -timeout $(E2E_TIMEOUT) $(GINKGO_OPTS) ./test/e2e -- -namespace=$(E2E_TEST_NS) -olmNamespace=$(E2E_INSTALL_NS) -catalogNamespace=$(E2E_CATALOG_NS) $(E2E_OPTS)

.PHONY: e2e-local
e2e-local: e2e-build kind-create deploy e2e

#SECTION Code Generation

.PHONY: gen-all
gen-all: manifests codegen mockgen

# Copy CRD manifests
.PHONY: manifests
manifests: vendor
	./scripts/copy_crds.sh

# Generate deepcopy, conversion, clients, listers, and informers
.PHONY: codegen
codegen:
	# Clients, listers, and informers
	./scripts/update_codegen.sh

# Generate mock types.
.PHONY: mockgen
mockgen:
	./scripts/update_mockgen.sh

#SECTION Verification

.PHONY: diff
diff:
	git diff --exit-code

.PHONY: verify-codegen
verify-codegen: codegen
	$(MAKE) diff

.PHONY: verify-mockgen
verify-mockgen: mockgen
	$(MAKE) diff

.PHONY: verify-manifests
verify-manifests: manifests
	$(MAKE) diff

.PHONY: verify-vendor
verify: vendor verify-codegen verify-mockgen verify-manifests

#SECTION Release

.PHONY: pull-opm
pull-opm:
	docker pull $(OPERATOR_REGISTRY_IMAGE)

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
release: ver=v$(shell cat OLM_VERSION)
# pull the opm image to get the digest
release: pull-opm manifests
	@echo "Generating the $(ver) release"
	docker pull $(IMAGE_REPO):$(ver)
	$(MAKE) target=upstream ver=$(ver) quickstart=true package

package: olmref=$(shell docker inspect --format='{{index .RepoDigests 0}}' $(IMAGE_REPO):$(ver))
package: opmref=$(shell docker inspect --format='{{index .RepoDigests 0}}' $(OPERATOR_REGISTRY_IMAGE))
package:
ifndef target
	$(error target is undefined)
endif
ifndef ver
	$(error ver is undefined)
endif
	@echo "Getting operator registry image"
	docker pull $(OPERATOR_REGISTRY_IMAGE)
	$(YQ) w -i deploy/$(target)/values.yaml olm.image.ref $(olmref)
	$(YQ) w -i deploy/$(target)/values.yaml catalog.image.ref $(olmref)
	$(YQ) w -i deploy/$(target)/values.yaml package.image.ref $(olmref)
	$(YQ) w -i deploy/$(target)/values.yaml -- catalog.opmImageArgs "--opmImage=$(opmref)"
	./scripts/package_release.sh $(ver) deploy/$(target)/manifests/$(ver) deploy/$(target)/values.yaml
	ln -sfFn ./$(ver) deploy/$(target)/manifests/latest
ifeq ($(quickstart), true)
	./scripts/package_quickstart.sh deploy/$(target)/manifests/$(ver) deploy/$(target)/quickstart/olm.yaml deploy/$(target)/quickstart/crds.yaml deploy/$(target)/quickstart/install.sh
endif

################################
#  OLM - Install/Uninstall/Run #
################################

.PHONY: uninstall
uninstall:
	@echo Uninstalling OLM:
	- kubectl delete -f deploy/upstream/quickstart/crds.yaml
	- kubectl delete -f deploy/upstream/quickstart/olm.yaml
	- kubectl delete catalogsources.operators.coreos.com
	- kubectl delete clusterserviceversions.operators.coreos.com
	- kubectl delete installplans.operators.coreos.com
	- kubectl delete operatorgroups.operators.coreos.com subscriptions.operators.coreos.com
	- kubectl delete apiservices.apiregistration.k8s.io v1.packages.operators.coreos.com
	- kubectl delete ns olm
	- kubectl delete ns openshift-operator-lifecycle-manager
	- kubectl delete ns openshift-operators
	- kubectl delete ns operators
	- kubectl delete clusterrole.rbac.authorization.k8s.io/aggregate-olm-edit
	- kubectl delete clusterrole.rbac.authorization.k8s.io/aggregate-olm-view
	- kubectl delete clusterrole.rbac.authorization.k8s.io/system:controller:operator-lifecycle-manager
	- kubectl delete clusterroles.rbac.authorization.k8s.io "system:controller:operator-lifecycle-manager"
	- kubectl delete clusterrolebindings.rbac.authorization.k8s.io "olm-operator-binding-openshift-operator-lifecycle-manager"

.PHONY: FORCE
FORCE:
