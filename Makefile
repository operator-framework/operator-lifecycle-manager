##########################
#  OLM - Build and Test  #
##########################

# Undefine GOFLAGS environment variable.
ifdef GOFLAGS
$(warning Undefining GOFLAGS set in CI)
undefine GOFLAGS
endif

SHELL := /bin/bash
ORG := github.com/operator-framework
PKG   := $(ORG)/operator-lifecycle-manager
MOD_FLAGS := $(shell (go version | grep -q -E "1\.1[1-9]") && echo -mod=vendor)
CMDS  := $(shell go list $(MOD_FLAGS) ./cmd/...)
TCMDS := $(shell go list $(MOD_FLAGS) ./test/e2e/...)
MOCKGEN := ./scripts/update_mockgen.sh
CODEGEN := ./scripts/update_codegen.sh
IMAGE_REPO := quay.io/operator-framework/olm
IMAGE_TAG ?= "dev"
SPECIFIC_UNIT_TEST := $(if $(TEST),-run $(TEST),)
LOCAL_NAMESPACE := "olm"
export GO111MODULE=on
CONTROLLER_GEN := go run $(MOD_FLAGS) ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen
YQ_INTERNAL := go run $(MOD_FLAGS) ./vendor/github.com/mikefarah/yq/v2/
KUBEBUILDER_ASSETS ?= /usr/local/kubebuilder/bin

# ART builds are performed in dist-git, with content (but not commits) copied 
# from the source repo. Thus at build time if your code is inspecting the local
# git repo it is getting unrelated commits and tags from the dist-git repo, 
# not the source repo.
# For ART image builds, SOURCE_GIT_COMMIT, SOURCE_GIT_TAG, SOURCE_DATE_EPOCH 
# variables are inserted in Dockerfile to enable recovering the original git 
# metadata at build time.
GIT_COMMIT := $(if $(SOURCE_GIT_COMMIT),$(SOURCE_GIT_COMMIT),$(shell git rev-parse HEAD))

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e \
	kubebuilder .FORCE

all: test build

test: clean cover.out

unit: kubebuilder
	KUBEBUILDER_ASSETS=$(KUBEBUILDER_ASSETS) go test $(MOD_FLAGS) $(SPECIFIC_UNIT_TEST) -tags "json1" -v -race -count=1 ./pkg/...

# Ensure kubebuilder is installed before continuing
KUBEBUILDER_ASSETS_ERR := not detected in $(KUBEBUILDER_ASSETS), to override the assets path set the KUBEBUILDER_ASSETS environment variable, for install instructions see https://book.kubebuilder.io/quick-start.html
kubebuilder:
ifeq (, $(wildcard $(KUBEBUILDER_ASSETS)/kubebuilder))
	$(error kubebuilder $(KUBEBUILDER_ASSETS_ERR))
endif
ifeq (, $(wildcard $(KUBEBUILDER_ASSETS)/etcd))
	$(error etcd $(KUBEBUILDER_ASSETS_ERR))
endif
ifeq (, $(wildcard $(KUBEBUILDER_ASSETS)/kube-apiserver))
	$(error kube-apiserver $(KUBEBUILDER_ASSETS_ERR))
endif

schema-check:

cover.out: schema-check
	go test $(MOD_FLAGS) -v -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out
	go tool cover -func=cover.out

coverage-html: cover.out
	go tool cover -html=cover.out

build: build_cmd=build
build: clean $(CMDS)

test-bare: BUILD_TAGS=-tags=bare
test-bare: clean $(TCMDS)

test-bin: clean $(TCMDS)

# build versions of the binaries with coverage enabled
build-coverage: build_cmd=test -c -covermode=count -coverpkg ./pkg/controller/...
build-coverage: clean $(CMDS)

build-linux: build_cmd=build
build-linux: arch_flags=GOOS=linux GOARCH=386
build-linux: clean $(CMDS)

build-wait: clean bin/wait

bin/wait:
	GOOS=linux GOARCH=386 go build $(MOD_FLAGS) -o $@ $(PKG)/test/e2e/wait

build-util-linux: arch_flags=GOOS=linux GOARCH=386
build-util-linux: build-util

build-util: bin/cpb

bin/cpb:
	CGO_ENABLED=0 $(arch_flags) go build $(MOD_FLAGS) -ldflags '-extldflags "-static"' -o $@ ./util/cpb

$(CMDS): version_flags=-ldflags "-X $(PKG)/pkg/version.GitCommit=$(GIT_COMMIT) -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`"
$(CMDS):
	$(arch_flags) go $(build_cmd) $(MOD_FLAGS) $(version_flags) -tags "json1" -o bin/$(shell basename $@) $@

build: clean $(CMDS)

$(TCMDS):
	go test -c $(BUILD_TAGS) $(MOD_FLAGS) -o bin/$(shell basename $@) $@

deploy-local:
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build

e2e.namespace:
	@printf "e2e-tests-$(shell date +%s)-$$RANDOM" > e2e.namespace

# useful if running e2e directly with `go test -tags=bare`
setup-bare: clean e2e.namespace
	. ./scripts/build_bare.sh
	. ./scripts/package_release.sh 1.0.0 test/e2e/resources test/e2e/e2e-bare-values.yaml
	. ./scripts/install_bare.sh $(shell cat ./e2e.namespace) test/e2e/resources

# e2e test exculding the rh-operators directory which tests rh-operators and their metric cardinality.
e2e:
	go test -v $(MOD_FLAGS) -failfast -timeout 150m ./test/e2e/... -namespace=openshift-operators -kubeconfig=${KUBECONFIG} -olmNamespace=openshift-operator-lifecycle-manager -dummyImage=bitnami/nginx:latest

e2e-local: build-linux build-wait build-util-linux
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_local.sh $(TEST)

e2e-bare: setup-bare
	. ./scripts/run_e2e_bare.sh $(TEST)

e2e-local-docker:
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_docker.sh $(TEST)

e2e-operator-metrics:
	go test -v $(MOD_FLAGS) -timeout 70m ./test/rh-operators/...

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

verify-codegen: codegen diff
verify-mockgen: mockgen diff
verify-manifests: manifests diff
verify: verify-codegen verify-mockgen verify-manifests

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
release: ver=$(shell cat OLM_VERSION)
release: manifests
	docker pull quay.io/operator-framework/olm:$(ver)
	$(MAKE) target=upstream ver=$(ver) quickstart=true package
	$(MAKE) target=ocp ver=$(ver) package
	rm -rf manifests
	mkdir manifests
	cp -R deploy/ocp/manifests/$(ver)/. manifests
	# requires gnu sed if on mac
	find ./manifests -type f -exec sed -i "/^#/d" {} \;
	find ./manifests -type f -exec sed -i "1{/---/d}" {} \;

verify-release: release diff

package: olmref=$(shell docker inspect --format='{{index .RepoDigests 0}}' quay.io/operator-framework/olm:$(ver))
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

.PHONY: run-console-local
run-console-local:
	@echo Running script to run the OLM console locally:
	. ./scripts/run_console_local.sh

.PHONY: uninstall
uninstall:
	@echo Uninstalling OLM:
	- kubectl delete -f deploy/upstream/quickstart/crds.yaml
	- kubectl delete -f deploy/upstream/quickstart/olm.yam
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

.PHONY: run-local
run-local: build-linux build-wait build-util-linux
	rm -rf build
	. ./scripts/build_local.sh
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build