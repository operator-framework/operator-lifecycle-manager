##########################
#  OLM - Build and Test  #
##########################

SHELL := /bin/bash
PKG   := github.com/operator-framework/operator-lifecycle-manager
OS := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
MOD_FLAGS := $(shell (go version | grep -q -E "1\.1[1-9]") && echo -mod=vendor)
CMDS  := $(shell go list $(MOD_FLAGS) ./cmd/...)
TCMDS := $(shell go list $(MOD_FLAGS) ./test/e2e/...)
MOCKGEN := ./scripts/update_mockgen.sh
CODEGEN := ./scripts/update_codegen.sh
GEN_PATHS := "./pkg/api/apis/operators/v2alpha1/..."
IMAGE_REPO := quay.io/operator-framework/olm
IMAGE_TAG ?= "dev"
SPECIFIC_UNIT_TEST := $(if $(TEST),-run $(TEST),)
LOCAL_NAMESPACE := "olm"
export GO111MODULE=on

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# ART builds are performed in dist-git, with content (but not commits) copied 
# from the source repo. Thus at build time if your code is inspecting the local
# git repo it is getting unrelated commits and tags from the dist-git repo, 
# not the source repo.
# For ART image builds, SOURCE_GIT_COMMIT, SOURCE_GIT_TAG, SOURCE_DATE_EPOCH 
# variables are inserted in Dockerfile to enable recovering the original git 
# metadata at build time.
GIT_COMMIT := $(if $(SOURCE_GIT_COMMIT),$(SOURCE_GIT_COMMIT),$(shell git rev-parse HEAD))

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e manifests \
	controller-gen kubebuilder .FORCE

all: test build

test: clean cover.out

unit: kubebuilder
	$(KUBEBUILDER_ENV) go test $(MOD_FLAGS) $(SPECIFIC_UNIT_TEST) -v -race -tags=json1 -count=1 ./pkg/...

# Install kubebuilder if not found
kubebuilder:
ifeq (, $(shell which kubebuilder))
	@curl -sL https://go.kubebuilder.io/dl/2.0.1/$(OS)/$(ARCH) | tar -xz -C /tmp/
KUBEBUILDER_ENV := KUBEBUILDER_ASSETS=/tmp/kubebuilder_2.0.1_$(OS)_$(ARCH)/bin
endif

schema-check:

cover.out: schema-check
	go test $(MOD_FLAGS) -v -race -tags=json1 -coverprofile=cover.out -covermode=atomic \
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
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -o $@ $(PKG)/test/e2e/wait

$(CMDS): version_flags=-ldflags "-X $(PKG)/pkg/version.GitCommit=$(GIT_COMMIT) -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`"
$(CMDS):
	CGO_ENABLED=0 $(arch_flags) go $(build_cmd) $(MOD_FLAGS) $(version_flags) -o bin/$(shell basename $@) $@

$(TCMDS):
	CGO_ENABLED=0 go test -c $(BUILD_TAGS) $(MOD_FLAGS) -o bin/$(shell basename $@) $@

run-local: build-linux build-wait
	rm -rf build
	. ./scripts/build_local.sh
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources doc/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build

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

e2e:
	go test -v $(MOD_FLAGS) -failfast -timeout 70m ./test/e2e/... -namespace=openshift-operators -kubeconfig=${KUBECONFIG} -olmNamespace=openshift-operator-lifecycle-manager -dummyImage=bitnami/nginx:latest

e2e-local: build-linux build-wait
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_local.sh $(TEST)

e2e-bare: setup-bare
	. ./scripts/run_e2e_bare.sh $(TEST)

e2e-local-docker:
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_docker.sh $(TEST)

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

# Generate manifests for CRDs
# Use trivialVersions=true to generate CRDs w/o conversion webhooks for now
manifests: controller-gen
	$(CONTROLLER_GEN) crd paths=$(GEN_PATHS) output:crd:artifacts:config="config/crd/bases"

codegen: codegen-v2 codegen-v1

codegen-v1: vendor
	$(CODEGEN)

codegen-v2: controller-gen
	$(CONTROLLER_GEN) object:headerFile=boilerplate.go.txt paths=$(GEN_PATHS)

# Find or download controller-gen.
controller-gen:
ifeq (, $(shell which controller-gen))
CONTROLLER_GEN=go run -mod=vendor ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

verify-codegen: codegen
	git diff --exit-code

mockgen:
	$(MOCKGEN)

verify-mockgen: mockgen
	git diff --exit-code

verify: verify-codegen verify-mockgen verify-manifests

gen-all: codegen mockgen

# this is here for backwards compatibility with the ci job that calls verify-catalog
verify-catalog:

# this is here for backwards compatibility with the ci job that calls verify-manifests
verify-manifests:

verify-release: ver=$(shell cat OLM_VERSION)
verify-release:
	rm -rf manifests
	mkdir manifests
	./scripts/package_release.sh $(ver) manifests deploy/ocp/values.yaml
	git diff --exit-code

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
release: ver=$(shell cat OLM_VERSION)
release:
	docker pull quay.io/operator-framework/olm:$(ver)
	$(MAKE) target=upstream ver=$(ver) quickstart=true package
	$(MAKE) target=ocp ver=$(ver) package
	rm -rf manifests
	mkdir manifests
	cp -R deploy/ocp/manifests/$(ver)/. manifests

package: olmref=$(shell docker inspect --format='{{index .RepoDigests 0}}' quay.io/operator-framework/olm:$(ver))
package:
ifndef target
	$(error target is undefined)
endif
ifndef ver
	$(error ver is undefined)
endif
	yq w -i deploy/$(target)/values.yaml olm.image.ref $(olmref)
	yq w -i deploy/$(target)/values.yaml catalog.image.ref $(olmref)
	yq w -i deploy/$(target)/values.yaml package.image.ref $(olmref)
	./scripts/package_release.sh $(ver) deploy/$(target)/manifests/$(ver) deploy/$(target)/values.yaml
	ln -sfFn ./$(ver) deploy/$(target)/manifests/latest
ifeq ($(quickstart), true)
	./scripts/package_quickstart.sh deploy/$(target)/manifests/$(ver) deploy/$(target)/quickstart/olm.yaml deploy/$(target)/quickstart/crds.yaml deploy/$(target)/quickstart/install.sh
endif

##########################
#  OLM - Commands        #
##########################

.PHONY: run-console-local
run-console-local:
	@echo Running script to run the OLM console locally:
	. ./scripts/run_console_local.sh
