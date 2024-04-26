##########################
#  OLM - Build and Test  #
##########################

# setup-envtest on *nix uses XDG_DATA_HOME, falling back to HOME, as the default storage directory. Some CI setups
# don't have XDG_DATA_HOME set; in those cases, we set it here so setup-envtest functions correctly. This shouldn't
# affect developers.
export XDG_DATA_HOME ?= /tmp/.local/share

# bingo manages consistent tooling versions for things like kind, kustomize, etc.
include .bingo/Variables.mk

# Undefine GOFLAGS environment variable.
ifdef GOFLAGS
$(warning Undefining GOFLAGS set in CI)
undefine GOFLAGS
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec
ORG := github.com/operator-framework
PKG   := $(ORG)/operator-lifecycle-manager
MOD_FLAGS := -mod=vendor -buildvcs=false
BUILD_TAGS := "json1"
CMDS  := $(shell go list $(MOD_FLAGS) ./cmd/...)
TCMDS := $(shell go list $(MOD_FLAGS) ./test/e2e/...)
MOCKGEN := ./scripts/update_mockgen.sh
CODEGEN := ./scripts/update_codegen.sh
IMAGE_REPO := quay.io/operator-framework/olm
IMAGE_TAG ?= "dev"
SPECIFIC_UNIT_TEST := $(if $(TEST),-run $(TEST),)
LOCAL_NAMESPACE := "operator-lifecycle-manager"
export GO111MODULE=on
YQ_INTERNAL := go run $(MOD_FLAGS) ./vendor/github.com/mikefarah/yq/v3/
KUBEBUILDER_ASSETS := $(or $(or $(KUBEBUILDER_ASSETS),$(dir $(shell command -v kubebuilder))),/usr/local/kubebuilder/bin)
export KUBEBUILDER_ASSETS
GO := GO111MODULE=on GOFLAGS="$(MOD_FLAGS)" go
GINKGO := $(GO) run github.com/onsi/ginkgo/v2/ginkgo
BINDATA := $(GO) run github.com/go-bindata/go-bindata/v3/go-bindata
GIT_COMMIT := $(shell git rev-parse HEAD)
ifeq ($(shell arch), arm64) 
ARCH := arm64
else
ARCH := 386
endif

# kind cluster configuration
KIND_CLUSTER_NAME ?= olmv0
# Not guaranteed to have patch releases available and node image tags are full versions (i.e v1.28.0 - no v1.28, v1.29, etc.)
# The KIND_NODE_VERSION is set by getting the version of the k8s.io/client-go dependency from the go.mod
# and sets major version to "1" and the patch version to "0". For example, a client-go version of v0.28.5
# will map to a KIND_NODE_VERSION of 1.28.0
KIND_NODE_VERSION = $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1.0/')
KIND_CLUSTER_IMAGE ?= kindest/node:v${KIND_NODE_VERSION}

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Phony prerequisite for targets that rely on the go build cache to determine staleness.
.PHONY: build test clean vendor \
	coverage coverage-html e2e \
	kubebuilder

.PHONY: FORCE
FORCE:

# Disable -j flag for make
.NOTPARALLEL:

.DEFAULT_GOAL := help-extended

#SECTION Build

.PHONY: build
build: build_cmd=build #HELP Build the OLM binaries.
build: clean $(CMDS)

container: #HELP Build the OLM container image.
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

# Determine setup_envtest version based on the client-go version in go.mod
ENVTEST_VERSION = $(shell go list -m k8s.io/client-go | cut -d" " -f2 | sed 's/^v0\.\([[:digit:]]\{1,\}\)\.[[:digit:]]\{1,\}$$/1.\1.x/')
unit: $(SETUP_ENVTEST) #HELP Run unit tests.
	eval $$($(SETUP_ENVTEST) use -p env $(ENVTEST_VERSION)) && go test $(MOD_FLAGS) $(SPECIFIC_UNIT_TEST) -tags "json1" -race -count=1 ./pkg/... ./test/e2e/split/...

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
E2E_OPTS ?= $(if $(E2E_SEED),-seed '$(E2E_SEED)') $(if $(SKIP), -skip '$(SKIP)') $(if $(TEST),-focus '$(TEST)') $(if $(ARTIFACT_DIR), -output-dir $(ARTIFACT_DIR) -junit-report junit_e2e.xml) -flake-attempts $(E2E_FLAKE_ATTEMPTS) -nodes $(E2E_NODES) -timeout $(E2E_TIMEOUT) -v -randomize-suites -race -trace -progress
E2E_INSTALL_NS ?= operator-lifecycle-manager
E2E_CATALOG_NS ?= $(E2E_INSTALL_NS)
E2E_TEST_NS ?= operators

clean: #HELP Clean up build artifacts.
	@rm -rf cover.out
	@rm -rf bin
	@rm -rf test/e2e/resources
	@rm -rf test/e2e/test-resources
	@rm -rf test/e2e/log
	@rm -rf e2e.namespace

#SECTION Generate

# Generates everything.
generate: codegen mockgen manifests #HELP Generate everything.

# Copy CRD manifests
manifests: vendor #EXHELP Copy CRD manifests.
	./scripts/copy_crds.sh

# Generate deepcopy, conversion, clients, listers, and informers
codegen: #EXHELP Generate deepcopy, conversion, clients, listers, and informers.
	# Clients, listers, and informers
	$(CODEGEN)

# Generate mock types.
mockgen: #EXHELP Generate mock types.
	$(MOCKGEN)

#SECTION Deploy

.PHONY: kind-cluster
kind-cluster: $(KIND) #HELP Standup a kind cluster.
	-$(KIND) delete cluster --name ${KIND_CLUSTER_NAME}
	# kind-config.yaml can be deleted after upgrading to Kubernetes 1.30
	$(KIND) create cluster --name ${KIND_CLUSTER_NAME} --image ${KIND_CLUSTER_IMAGE}
	$(KIND) export kubeconfig --name ${KIND_CLUSTER_NAME}

.PHONY: kind-clean
kind-clean: $(KIND) #HELP Delete the kind cluster.
	$(KIND) delete cluster --name ${KIND_CLUSTER_NAME}

.PHONY: build-local
build-local: build-linux build-wait build-util-linux
	rm -rf build
	. ./scripts/build_local.sh

.PHONY: deploy
deploy: build-local #HELP Deploy OLM locally.
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build

.PHONY: uninstall
uninstall: #HELP Uninstall OLM.
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

#SECTION End-to-End

e2e: #HELP Run end-to-end tests.
	NO_KIND=1 $(GINKGO) $(E2E_OPTS) $(or $(run), ./test/e2e) $< -- -namespace=$(E2E_TEST_NS) -olmNamespace=$(E2E_INSTALL_NS) -catalogNamespace=$(E2E_CATALOG_NS) -dummyImage=bitnami/nginx:latest $(or $(extra_args), -kubeconfig=${KUBECONFIG})

clean-e2e: #EXHELP Clean up e2e test resources.
	kubectl delete crds --all
	kubectl delete apiservices.apiregistration.k8s.io v1.packages.operators.coreos.com || true
	kubectl delete -f test/e2e/resources/0000_50_olm_00-namespace.yaml

#SECTION Tools

.PHONY: fmt
fmt: #EXHELP Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: #EXHELP Run go vet against code.
	go vet ./...

.PHONY: lint
lint: $(GOLANGCI_LINT) #HELP Run golangci linter.
	$(GOLANGCI_LINT) run --build-tags $(BUILD_TAGS) $(GOLANGCI_LINT_ARGS)

vendor: #HELP Update and Vendor dependencies.
	go mod tidy
	go mod vendor

#SECTION Verify

diff:
	git diff --exit-code

verify: verify-codegen verify-mockgen verify-manifests #HELP Verify everything.

verify-codegen: codegen #EXHELP Verify codegen.
	$(MAKE) diff

verify-mockgen: mockgen #EXHELP Verify mockgen.
	$(MAKE) diff

verify-manifests: manifests #EXHELP Verify manifests.
	$(MAKE) diff

#SECTION Release

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
release: ver=v$(shell cat OLM_VERSION) #HELP Generate a release.
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

#SECTION Legacy

all: test build #EXHELP Display extended help.

test: clean cover.out #EXHELP Run tests and generate a coverage report.

# Ensure kubectl installed before continuing
KUBEBUILDER_ASSETS_ERR := not detected in $(KUBEBUILDER_ASSETS), to override the assets path set the KUBEBUILDER_ASSETS environment variable, for install instructions see https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest
KUBECTL_ASSETS_ERR := kubectl not detected.
kubebuilder:
ifeq (, $(shell which kubectl))
	$(error $(KUBECTL_ASSETS_ERR))
endif
ifeq (, $(wildcard $(KUBEBUILDER_ASSETS)/etcd))
	$(error etcd $(KUBEBUILDER_ASSETS_ERR))
endif
ifeq (, $(wildcard $(KUBEBUILDER_ASSETS)/kube-apiserver))
	$(error kube-apiserver $(KUBEBUILDER_ASSETS_ERR))
endif

cover.out: #EXHELP Generate a coverage report.
	go test $(MOD_FLAGS) -tags "json1" -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out #EXHELP Display the coverage report.
	go tool cover -func=cover.out

coverage-html: cover.out #EXHELP Display the coverage report in HTML.
	go tool cover -html=cover.out

test-bare: BUILD_TAGS=-tags=bare #EXHELP Run tests with the bare tag.
test-bare: clean $(TCMDS)

test-bin: clean $(TCMDS) #EXHELP Run tests with the bare tag and build the test binaries.

# build versions of the binaries with coverage enabled
build-coverage: build_cmd=test -c -covermode=count -coverpkg ./pkg/controller/... #EXHELP Build the OLM binaries with coverage enabled.
build-coverage: clean $(CMDS)

build-linux: build_cmd=build #EXHELP Build the OLM binaries for Linux.
build-linux: arch_flags=GOOS=linux GOARCH=$(ARCH)
build-linux: clean $(CMDS)

build-wait: clean bin/wait #EXHELP Build the wait binary.

bin/wait: FORCE
	GOOS=linux GOARCH=$(ARCH) go build $(MOD_FLAGS) -o $@ $(PKG)/test/e2e/wait

build-util-linux: #EXHELP Build the OLM utility binaries for Linux.
	arch_flags=GOOS=linux GOARCH=$(ARCH)
build-util-linux: build-util

build-util: bin/cpb bin/copy-content #EXHELP Build the OLM utility binaries.

bin/cpb: FORCE #EXHELP Build the cpb binary.
	CGO_ENABLED=0 $(arch_flags) go build -buildvcs=false $(MOD_FLAGS) -ldflags '-extldflags "-static"' -o $@ ./util/cpb

bin/copy-content: FORCE #HELP Build the copy-content binary.
	CGO_ENABLED=0 $(arch_flags) go build -buildvcs=false $(MOD_FLAGS) -ldflags '-extldflags "-static"' -o $@ ./cmd/copy-content

$(CMDS): version_flags=-ldflags "-X $(PKG)/pkg/version.GitCommit=$(GIT_COMMIT) -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`"
$(CMDS):
	$(arch_flags) go $(build_cmd) $(MOD_FLAGS) $(version_flags) -tags $(BUILD_TAGS) -o bin/$(shell basename $@) $@

$(TCMDS):
	go test -c $(BUILD_TAGS) $(MOD_FLAGS) -o bin/$(shell basename $@) $@

deploy-local: #EXHELP Deploy OLM locally.
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build

e2e.namespace: #EXHELP Create a namespace for e2e tests.
	@printf "e2e-tests-$(shell date +%s)-$$RANDOM" > e2e.namespace

# useful if running e2e directly with `go test -tags=bare`
setup-bare: clean e2e.namespace #EXHELP Setup the environment for bare e2e tests.
	. ./scripts/build_bare.sh
	. ./scripts/package_release.sh 1.0.0 test/e2e/resources test/e2e/e2e-bare-values.yaml
	. ./scripts/install_bare.sh $(shell cat ./e2e.namespace) test/e2e/resources

# See workflows/e2e-tests.yml See test/e2e/README.md for details.
.PHONY: e2e-local #EXHELP Run end-to-end local tests.
e2e-local: BUILD_TAGS="json1 e2e experimental_metrics" #EXHELP Run end-to-end tests locally.
e2e-local: extra_args=-kind.images=../test/e2e-local.image.tar -test-data-dir=../test/e2e/testdata -gather-artifacts-script-path=../test/e2e/collect-ci-artifacts.sh
e2e-local: run=bin/e2e-local.test
e2e-local: bin/e2e-local.test test/e2e-local.image.tar
e2e-local: e2e

# this target updates the zz_chart.go file with files found in deploy/chart
# this will always fire since it has been marked as phony
.PHONY: test/e2e/assets/chart/zz_chart.go #EXHELP Update the chart assets.
test/e2e/assets/chart/zz_chart.go:
	$(shell find deploy/chart -type f)
	$(BINDATA) -o $@ -pkg chart -prefix deploy/chart/ $^

# execute kind and helm end to end tests
bin/e2e-local.test: FORCE test/e2e/assets/chart/zz_chart.go #EXHELP Build the e2e-local.test binary.
	$(GO) test -c -tags kind,helm -o $@ ./test/e2e

# set go env and other vars, ensure that the dockerfile exists, and then build wait, cpb, and other command binaries and finally the kind image archive
test/e2e-local.image.tar: export GOOS=linux #EXHELP Build the e2e-local.image.tar image.
test/e2e-local.image.tar: export GOARCH=386
test/e2e-local.image.tar: build_cmd=build
test/e2e-local.image.tar: e2e.Dockerfile bin/wait bin/cpb $(CMDS) #EXHELP Build the e2e-local.image.tar image.
	docker build -t quay.io/operator-framework/olm:local -f $< bin
	docker save -o $@ quay.io/operator-framework/olm:local

e2e-bare: setup-bare #EXHELP Run end-to-end tests with the bare tag.
	. ./scripts/run_e2e_bare.sh $(TEST)

e2e-local-docker: #EXHELP Run end-to-end tests locally in a Docker container.
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_docker.sh $(TEST)


#SECTION Help

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '#SECTION' and the
# target descriptions by '#HELP' or '#EXHELP'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: #HELP something, and then pretty-format the target and help. Then,
# if there's a line with #SECTION something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php
# The extended-help target uses '#EXHELP' as the delineator.

.PHONY: help
help: #HELP Display essential help.
	@awk 'BEGIN {FS = ":[^#]*#HELP"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\n"} /^[a-zA-Z_0-9-]+:.*#HELP / { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } ' $(MAKEFILE_LIST)

.PHONY: help-extended
help-extended: #HELP Display extended help.
	@awk 'BEGIN {FS = ":.*#(EX)?HELP"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*#(EX)?HELP / { printf "  \033[36m%-25s\033[0m %s\n", $$1, $$2 } /^#SECTION / { printf "\n\033[1m%s\033[0m\n", substr($$0, 10) } ' $(MAKEFILE_LIST)
