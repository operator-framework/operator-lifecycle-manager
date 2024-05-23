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

SHELL := /bin/bash
ORG := github.com/operator-framework
PKG   := $(ORG)/operator-lifecycle-manager
MOD_FLAGS := -mod=vendor -buildvcs=false
BUILD_TAGS := "json1"
CMDS  := $(shell go list $(MOD_FLAGS) ./cmd/...)
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

# Phony prerequisite for targets that rely on the go build cache to determine staleness.
.PHONY: build test clean vendor \
	coverage coverage-html e2e \
	kubebuilder

.PHONY: FORCE
FORCE:

.PHONY: vet
vet:
	go vet $(MOD_FLAGS) ./...

all: test build

test: clean cover.out
.PHONY: unit
KUBEBUILDER_ASSETS ?= $(shell $(SETUP_ENVTEST) use -p path $(ENVTEST_KUBE_VERSION))
unit:
	@echo "Running unit tests with setup_envtest for kubernetes $(ENVTEST_KUBE_VERSION)"
	KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) go test $(MOD_FLAGS) $(SPECIFIC_UNIT_TEST) -tags "json1" -race -count=1 ./pkg/... ./test/e2e/split/...

cover.out:
	go test $(MOD_FLAGS) -tags "json1" -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out
	go tool cover -func=cover.out

coverage-html: cover.out
	go tool cover -html=cover.out

build: build_cmd=build
build: clean vet $(CMDS)

# build versions of the binaries with coverage enabled
build-coverage: build_cmd=test -c -covermode=count -coverpkg ./pkg/controller/...
build-coverage: clean $(CMDS)

build-linux: build_cmd=build
build-linux: arch_flags=GOOS=linux GOARCH=$(ARCH)
build-linux: clean $(CMDS)

build-wait: clean bin/wait

bin/wait: FORCE
	GOOS=linux GOARCH=$(ARCH) go build $(MOD_FLAGS) -o $@ $(PKG)/test/e2e/wait

build-util-linux: arch_flags=GOOS=linux GOARCH=$(ARCH)
build-util-linux: build-util

build-util: bin/cpb bin/copy-content

bin/cpb: FORCE
	CGO_ENABLED=0 $(arch_flags) go build -buildvcs=false $(MOD_FLAGS) -ldflags '-extldflags "-static"' -o $@ ./util/cpb

bin/copy-content: FORCE
	CGO_ENABLED=0 $(arch_flags) go build -buildvcs=false $(MOD_FLAGS) -ldflags '-extldflags "-static"' -o $@ ./cmd/copy-content

$(CMDS): version_flags=-ldflags "-X $(PKG)/pkg/version.GitCommit=$(GIT_COMMIT) -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`"
$(CMDS):
	$(arch_flags) go $(build_cmd) $(MOD_FLAGS) $(version_flags) -tags $(BUILD_TAGS) -o bin/$(shell basename $@) $@

build: clean $(CMDS)

deploy-local:
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build

e2e.namespace:
	@printf "e2e-tests-$(shell date +%s)-$$RANDOM" > e2e.namespace

.PHONY: e2e
GINKGO_E2E_OPTS += -timeout 90m -v -randomize-suites -race -trace --show-node-events
E2E_OPTS += -namespace=operators -olmNamespace=operator-lifecycle-manager -catalogNamespace=operator-lifecycle-manager -dummyImage=bitnami/nginx:latest
e2e:
	$(GINKGO) $(GINKGO_E2E_OPTS) ./test/e2e -- $(E2E_OPTS)

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
	$(HELM) install olm deploy/chart \
		--set debug=true \
		--set olm.image.ref=$(OLM_IMAGE) \
		--set olm.image.pullPolicy=IfNotPresent \
		--set catalog.image.ref=$(OLM_IMAGE) \
		--set catalog.image.pullPolicy=IfNotPresent \
		--set package.image.ref=$(OLM_IMAGE) \
		--set package.image.pullPolicy=IfNotPresent \
		$(HELM_INSTALL_OPTS) \
		--wait;

.PHONY: e2e-build
e2e-build: BUILD_TAGS="json1 e2e experimental_metrics"
e2e-build: export GOOS=linux
e2e-build: export GOARCH=amd64
e2e-build: build_cmd=build
e2e-build: e2e.Dockerfile bin/wait bin/cpb $(CMDS)
	docker build -t quay.io/operator-framework/olm:local -f $< bin

vendor:
	go mod tidy
	go mod vendor

container:
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

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
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
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml olm.image.ref $(olmref)
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml catalog.image.ref $(olmref)
	$(YQ_INTERNAL) w -i deploy/$(target)/values.yaml package.image.ref $(olmref)
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

.PHONY: build-local
build-local: build-linux build-wait build-util-linux
	rm -rf build
	. ./scripts/build_local.sh

.PHONY: run-local
run-local: build-local
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build
