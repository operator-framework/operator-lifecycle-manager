##########################
#  OLM - Build and Test  #
##########################
# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL := /usr/bin/env bash -o pipefail
.SHELLFLAGS := -ec

# Undefine GOFLAGS environment variable.
ifdef GOFLAGS
$(warning Undefining GOFLAGS set in CI)
undefine GOFLAGS
endif

ORG := github.com/operator-framework
PKG   := $(ORG)/operator-lifecycle-manager
MOD_FLAGS := -mod=vendor -buildvcs=false
BUILD_TAGS := "json1"
MOCKGEN := ./scripts/update_mockgen.sh
CODEGEN := ./scripts/update_codegen.sh
IMAGE_REPO := quay.io/operator-framework/olm
IMAGE_TAG ?= "dev"
SPECIFIC_UNIT_TEST := $(if $(TEST),-run $(TEST),)
LOCAL_NAMESPACE := "olm"
export GO111MODULE=on
YQ_INTERNAL := go run $(MOD_FLAGS) ./vendor/github.com/mikefarah/yq/v3/
HELM := go run $(MOD_FLAGS) ./vendor/helm.sh/helm/v3/cmd/helm
KIND := go run $(MOD_FLAGS) ./vendor/sigs.k8s.io/kind
GO := GO111MODULE=on GOFLAGS="$(MOD_FLAGS)" go
GINKGO := $(GO) run github.com/onsi/ginkgo/v2/ginkgo
BINDATA := $(GO) run github.com/go-bindata/go-bindata/v3/go-bindata
SETUP_ENVTEST := $(GO) run sigs.k8s.io/controller-runtime/tools/setup-envtest
GIT_COMMIT := $(shell git rev-parse HEAD)
ifeq ($(shell arch), arm64) 
ARCH := arm64
else
ARCH := amd64
endif

# Track the minor version of kubernetes we are building against by looking at the client-go dependency version
# For example, a client-go version of v0.28.5 will map to kube version 1.28
KUBE_MINOR ?= $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1/')

# Unit test against the latest available version for the minor version of kubernetes we are building against e.g. 1.30.x
ENVTEST_KUBE_VERSION ?= $(KUBE_MINOR).x

# Kind node image tags are in the format x.y.z we pin to version x.y.0 because patch releases and node images
# are not guaranteed to be available when a new version of the kube apis is released
KIND_NODE_VERSION ?= $(KUBE_MINOR).0
KIND_CLUSTER_NAME ?= kind-olmv0
KIND_CLUSTER_IMAGE := kindest/node:v$(KIND_NODE_VERSION)

# Take operator registry tag from operator registry version in go.mod
export OPERATOR_REGISTRY_TAG ?= $(shell go list -m github.com/operator-framework/operator-registry | cut -d" " -f2)

# Pin operator registry images to the OPERATOR_REGISTRY_TAG
export OPERATOR_REGISTRY_IMAGE ?= quay.io/operator-framework/opm:$(OPERATOR_REGISTRY_TAG)
export CONFIGMAP_SERVER_IMAGE ?= quay.io/operator-framework/configmap-operator-registry:$(OPERATOR_REGISTRY_TAG)

# Phony prerequisite for targets that rely on the go build cache to determine staleness.
.PHONY: build test clean vendor \
	coverage coverage-html e2e \
	kubebuilder

all: test build

.PHONY: FORCE
FORCE:

ifeq ($(origin CGO_ENABLED), undefined)
CGO_ENABLED := 0
endif
export CGO_ENABLED

export GIT_REPO := $(shell go list -m)
export VERSION := $(shell cat OLM_VERSION)
export VERSION_PATH := ${GIT_REPO}/pkg/version
export GO_BUILD_ASMFLAGS := all=-trimpath=$(PWD)
export GO_BUILD_GCFLAGS := all=-trimpath=$(PWD)
export GO_BUILD_FLAGS := -mod=vendor -buildvcs=false
export GO_BUILD_LDFLAGS := -s -w -X '$(VERSION_PATH).version=$(VERSION)' -X '$(VERSION_PATH).gitCommit=$(GIT_COMMIT)' -extldflags "-static"
export GO_BUILD_TAGS := json1

BUILDCMD = go build $(GO_BUILD_FLAGS) -ldflags '$(GO_BUILD_LDFLAGS)' -tags '$(GO_BUILD_TAGS)' -gcflags '$(GO_BUILD_GCFLAGS)' -asmflags '$(GO_BUILD_ASMFLAGS)'

bin/cpb: FORCE
	CGO_ENABLED=0 $(arch_flags) go build -buildvcs=false $(MOD_FLAGS) -ldflags '-extldflags "-static"' -o $@ ./util/cpb

CMDS := $(shell go list $(MOD_FLAGS) ./cmd/...)
$(CMDS): FORCE
	@echo "Building $(@)"
	$(BUILDCMD) -o ./bin/$(shell basename $@) ./cmd/$(notdir $@)

.PHONY: build
build: bin/cpb $(CMDS)

test: clean cover.out
.PHONY: unit
KUBEBUILDER_ASSETS ?= $(shell $(SETUP_ENVTEST) use -p path $(ENVTEST_KUBE_VERSION))
unit:
	@echo "Running unit tests with setup_envtest for kubernetes $(ENVTEST_KUBE_VERSION)"
	CGO_ENABLED=1 KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) go test $(MOD_FLAGS) $(SPECIFIC_UNIT_TEST) -tags "json1" -race -count=1 ./pkg/... ./test/e2e/split/...

cover.out:
	go test $(MOD_FLAGS) -tags "json1" -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out
	go tool cover -func=cover.out

coverage-html: cover.out
	go tool cover -html=cover.out

deploy-local:
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build

.PHONY: e2e
E2E_TIMEOUT ?= 90m
E2E_TEST_NS ?= operators
E2E_INSTALL_NS ?= operator-lifecycle-manager
E2E_CATALOG_NS ?= $(E2E_INSTALL_NS)
GINKGO_OPTS += -v -randomize-suites -race -trace --show-node-events --flake-attempts=3
e2e:
	CGO_ENABLED=1 $(GINKGO) -timeout $(E2E_TIMEOUT) $(GINKGO_OPTS) ./test/e2e -- -namespace=$(E2E_TEST_NS) -olmNamespace=$(E2E_INSTALL_NS) -catalogNamespace=$(E2E_CATALOG_NS) $(E2E_OPTS)

.PHONY: e2e-local
e2e-local: e2e-build kind-create deploy e2e

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

.PHONY: e2e-build
e2e-build: export GO_BUILD_TAGS += e2e experimental_metrics
e2e-build: Dockerfile build
	docker build -t quay.io/operator-framework/olm:local -f $< bin

vendor:
	go mod tidy
	go mod vendor

container: build
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

clean-e2e:
	kubectl delete crds --all
	kubectl delete apiservices.apiregistration.k8s.io v1.packages.operators.coreos.com || true
	kubectl delete -f test/e2e/resources/0000_50_olm_00-namespace.yaml

clean:
	@rm -rf cover.out
	@rm -rf bin
	@rm -rf test/e2e/resources
	@rm -rf test/e2e/test-resources
	@rm -rf test/e2e/log
	@rm -rf e2e.namespace

.PHONY: vet
vet:
	go vet $(MOD_FLAGS) ./...

# Copy CRD manifests
manifests: vendor
	./scripts/copy_crds.sh

# Generate deepcopy, conversion, clients, listers, and informers
codegen:
	# Clients, listers, and informers
	$(CODEGEN)

# Generate mock types.
mockgen:
	$(MOCKGEN)

# Generates everything.
gen-all: codegen mockgen manifests

diff:
	git diff --exit-code

verify-codegen: codegen
	$(MAKE) diff

verify-mockgen: mockgen
	$(MAKE) diff

verify-manifests: manifests
	$(MAKE) diff

verify: vendor verify-codegen verify-mockgen verify-manifests

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
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml olm.image.ref $(olmref)
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml catalog.image.ref $(olmref)
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml package.image.ref $(olmref)
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml -- catalog.opmImageArgs "--opmImage=$(opmref)"
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
