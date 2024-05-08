##########################
#  OLM - Build and Test  #
##########################

# Undefine GOFLAGS environment variable.
ifdef GOFLAGS
$(warning Undefining GOFLAGS set in CI)
undefine GOFLAGS
endif

# bingo manages consistent tooling versions for things like kind, kustomize, etc.
include .bingo/Variables.mk

# set container runtime
ifneq (, $(shell command -v docker 2>/dev/null))
CONTAINER_RUNTIME := docker
else ifneq (, $(shell command -v podman 2>/dev/null))
CONTAINER_RUNTIME := podman
else
$(error Could not find docker or podman in path!)
endif

SHELL := /bin/bash

MOCKGEN := ./scripts/update_mockgen.sh
CODEGEN := ./scripts/update_codegen.sh
GINKGO := go run github.com/onsi/ginkgo/v2/ginkgo

# build settings

# Default CGO_ENABLED setting if not already set
CGO_ENABLED ?= 0
export CGO_ENABLED

# Dynamic version and commit data
export PKG := github.com/operator-framework/operator-lifecycle-manager
export GIT_COMMIT := $(shell git rev-parse HEAD)
export OLM_VERSION := $(shell cat OLM_VERSION)

# Linker and build flags
export GO_BUILD_LDFLAGS := -s -w -X $(PKG)/pkg/version.GitCommit=$(GIT_COMMIT) -X $(PKG)/pkg/version.OLMVersion=$(OLM_VERSION)
export GO_BUILD_FLAGS := -mod=vendor
export GO_BUILD_EXT_LDFLAGS ?=

MOD_FLAGS := -mod=vendor -buildvcs=false
BUILD_TAGS := json1

BUILDCMD = go build $(GO_BUILD_FLAGS) -ldflags '$(GO_BUILD_LDFLAGS) $(GO_BUILD_EXT_LDFLAGS)'

.PHONY: build-local
build-local:
	$(BUILDCMD) -o bin/olm ./cmd/olm
	$(BUILDCMD) -o bin/catalog ./cmd/catalog
	$(BUILDCMD) -o bin/package-server ./cmd/package-server

.PHONY: build-util
build-util: GO_BUILD_EXT_LDFLAGS := -extldflags "-static"
build-util:
	$(BUILDCMD) -o bin/copy-content ./cmd/copy-content
	$(BUILDCMD) -o bin/cpb ./util/cpb

.PHONY: build build-linux
build: clean build-local build-util
build-linux:
	export GOOS=linux; $(MAKE) build

.PHONY: build-wait
build-wait: clean bin/wait

bin/wait: FORCE
	GOOS=linux go build $(MOD_FLAGS) -o $@ $(PKG)/test/e2e/wait

# unit-test settings
# By default setup-envtest will write to $XDG_DATA_HOME, or $HOME/.local/share if that is not defined.
# If $HOME is not set, we need to specify a binary directory to prevent an error in setup-envtest.
# Useful for some CI/CD environments that set neither $XDG_DATA_HOME nor $HOME.
SETUP_ENVTEST_BIN_DIR_OVERRIDE=
ifeq ($(shell [[ $$HOME == "" || $$HOME == "/" ]] && [[ $$XDG_DATA_HOME == "" ]] && echo true ), true)
	SETUP_ENVTEST_BIN_DIR_OVERRIDE += --bin-dir /tmp/envtest-binaries
endif
ENVTEST_VERSION := $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1.x/')

.PHONY: unit
unit: $(SETUP_ENVTEST)
	eval $$($(SETUP_ENVTEST) use -p env $(ENVTEST_VERSION) $(SETUP_ENVTEST_BIN_DIR_OVERRIDE)) && CGO_ENABLED=1 go test $(MOD_FLAGS) $(if $(TEST),-run $(TEST),) -tags "json1" -race -count=1 ./pkg/... ./test/e2e/split/...

# e2e test settings
E2E_NODES ?= 1
E2E_FLAKE_ATTEMPTS ?= 1
E2E_TIMEOUT ?= 90m
# Optionally run an individual chunk of e2e test specs.
# Do not use this from the CLI; this is intended to be used by CI only.
E2E_TEST_CHUNK ?= all
E2E_TEST_NUM_CHUNKS ?= 4
ifneq (all,$(E2E_TEST_CHUNK))
TEST := $(shell go run ./test/e2e/split/... -chunks $(E2E_TEST_NUM_CHUNKS) -print-chunk $(E2E_TEST_CHUNK) ./test/e2e)
endif
E2E_OPTS ?= $(if $(E2E_SEED),-seed '$(E2E_SEED)') $(if $(SKIP), -skip '$(SKIP)') $(if $(TEST),-focus '$(TEST)') $(if $(ARTIFACT_DIR), -output-dir $(ARTIFACT_DIR) -junit-report junit_e2e.xml) -flake-attempts $(E2E_FLAKE_ATTEMPTS) -nodes $(E2E_NODES) -timeout $(E2E_TIMEOUT) -v -randomize-suites -trace --show-node-events
E2E_INSTALL_NS ?= operator-lifecycle-manager
E2E_CATALOG_NS ?= $(E2E_INSTALL_NS)
E2E_TEST_NS ?= operators

.PHONY: e2e
e2e:
	$(GINKGO) $(E2E_OPTS) $(or $(run), ./test/e2e) $< -- -namespace=$(E2E_TEST_NS) -olmNamespace=$(E2E_INSTALL_NS) -catalogNamespace=$(E2E_CATALOG_NS) -dummyImage=bitnami/nginx:latest $(or $(extra_args), -kubeconfig=${KUBECONFIG})

# See workflows/e2e-tests.yml See test/e2e/README.md for details.
.PHONY: e2e-local
e2e-local: BUILD_TAGS="json1 e2e experimental_metrics"
e2e-local: extra_args=-test-data-dir=./testdata -gather-artifacts-script-path=./collect-ci-artifacts.sh
e2e-local: e2e

# cluster provisioning settings
# e2e and local development kind cluster settings
KIND_CLUSTER_NAME := kind-olmv0
# Not guaranteed to have patch releases available and node image tags are full versions (i.e v1.28.0 - no v1.28, v1.29, etc.)
# The KIND_NODE_VERSION is set by getting the version of the k8s.io/client-go dependency from the go.mod
# and sets major version to "1" and the patch version to "0". For example, a client-go version of v0.28.5
# will map to a KIND_NODE_VERSION of 1.28.0
KIND_NODE_VERSION := $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1.0/')
KIND_CLUSTER_IMAGE := kindest/node:v$(KIND_NODE_VERSION)

.PHONY: kind-create
kind-create: $(KIND)
	$(KIND) delete cluster --name $(KIND_CLUSTER_NAME)
	$(KIND) create cluster --name $(KIND_CLUSTER_NAME) --image $(KIND_CLUSTER_IMAGE)
	$(KIND) export kubeconfig --name $(KIND_CLUSTER_NAME)

.PHONY: kind-load
kind-load: $(KIND)
	$(KIND) load docker-image $(IMAGE_REPO):$(IMAGE_TAG) --name $(KIND_CLUSTER_NAME)

.PHONY: deploy
deploy: $(HELM)
	$(HELM) install olm deploy/chart \
		--set debug=true \
		--set olm.image.ref=$(IMAGE_REPO):$(IMAGE_TAG) \
		--set olm.image.pullPolicy=IfNotPresent \
		--set catalog.image.ref=$(IMAGE_REPO):$(IMAGE_TAG) \
		--set catalog.image.pullPolicy=IfNotPresent \
		--set package.image.ref=$(IMAGE_REPO):$(IMAGE_TAG) \
		--set package.image.pullPolicy=IfNotPresent \
		--wait;

.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor

# container settings
IMAGE_REPO ?= quay.io/operator-framework/olm
IMAGE_TAG ?= local

.PHONY: container
container:
	$(CONTAINER_RUNTIME) build -t $(IMAGE_REPO):$(IMAGE_TAG) -f Dockerfile ./bin

.PHONY: clean-e2e
clean-e2e:
	kubectl delete crds --all
	kubectl delete apiservices.apiregistration.k8s.io v1.packages.operators.coreos.com || true
	kubectl delete -f test/e2e/resources/0000_50_olm_00-namespace.yaml

.PHONY: clean
clean:
	@rm -rf cover.out
	@rm -rf bin
	@rm -rf test/e2e/resources
	@rm -rf test/e2e/test-resources
	@rm -rf test/e2e/log
	@rm -rf e2e.namespace

# Copy CRD manifests
.PHONY: manifests
manifests: vendor
	./scripts/copy_crds.sh

# Generate deepcopy, conversion, clients, listers, and informers
.PHONY: codegen
codegen:
	# Clients, listers, and informers
	$(CODEGEN)

# Generate mock types.
.PHONY: mockgen
mockgen:
	$(MOCKGEN)

# Generates everything.
.PHONY: gen-all
gen-all: codegen mockgen manifests

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

.PHONY: verify
verify: verify-codegen verify-mockgen verify-manifests

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
.PHONY: release
release: ver=v$(shell cat OLM_VERSION)
release: manifests
	@echo "Generating the $(ver) release"
	docker pull $(IMAGE_REPO):$(ver)
	$(MAKE) target=upstream ver=$(ver) quickstart=true package

package: olmref=$(shell docker inspect --format='{{index .RepoDigests 0}}' $(IMAGE_REPO):$(ver))
package:
ifndef target
	$(error target is undefined)
endif
ifndef ver
	$(error ver is undefined)
endif
	$(YQ_V3) w -i deploy/$(target)/values.yaml olm.image.ref $(olmref)
	$(YQ_V3) w -i deploy/$(target)/values.yaml catalog.image.ref $(olmref)
	$(YQ_V3) w -i deploy/$(target)/values.yaml package.image.ref $(olmref)
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
