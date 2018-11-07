##########################
#  OLM - Build and Test  #
##########################

SHELL := /bin/bash
PKG   := github.com/operator-framework/operator-lifecycle-manager
CMDS  := $(addprefix bin/, $(shell go list ./cmd/... | xargs -I{} basename {}))
IMAGE_REPO := quay.io/coreos/olm
IMAGE_TAG ?= "dev"

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e .FORCE

all: test build

test: schema-check cover.out

unit:
	go test -mod=vendor -v -race ./pkg/...

schema-check:
	go run -mod=vendor ./cmd/validator/main.go ./deploy/chart/catalog_resources

cover.out: schema-check
	go test -mod=vendor -v -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out
	go tool cover -func=cover.out

coverage-html: cover.out
	go tool cover -html=cover.out

build: build_cmd=build
build: clean $(CMDS)

# build versions of the binaries with coverage enabled
build-coverage: build_cmd=test -c -covermode=count -coverpkg ./pkg/controller/...
build-coverage: clean $(CMDS)

$(CMDS): mod_flags=$(shell [ -f go.mod ] && echo -mod=vendor)
$(CMDS): version_flags=-ldflags "-w -X $(PKG)/pkg/version.GitCommit=`git rev-parse --short HEAD` -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`"
$(CMDS):
	CGO_ENABLED=0 go $(build_cmd) $(mod_flags) $(version_flags) -o $@ $(PKG)/cmd/$(shell basename $@);

run-local:
	. ./scripts/build_local.sh
	mkdir -p build/resources
	. ./scripts/package-release.sh 1.0.0-local build/resources Documentation/install/local-values.yaml
	. ./scripts/install_local.sh local build/resources
	rm -rf build

deploy-local:
	mkdir -p build/resources
	. ./scripts/package-release.sh 1.0.0-local build/resources Documentation/install/local-values.yaml
	. ./scripts/install_local.sh local build/resources
	rm -rf build

run-local-shift:
	. ./scripts/build_local_shift.sh
	mkdir -p build/resources
	. ./scripts/package-release.sh 1.0.0-local build/resources Documentation/install/local-values-shift.yaml
	. ./scripts/install_local.sh local build/resources
	rm -rf build

e2e-local:
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_local.sh $(TEST)

e2e-local-shift:
	. ./scripts/build_local_shift.sh
	. ./scripts/run_e2e_local.sh $(TEST)

e2e-local-docker:
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_docker.sh $(TEST)

vendor:
	go mod vendor

container: build
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

clean:
	rm -rf bin
	rm -rf test/e2e/resources
	rm -rf test/e2e/test-resources
	rm -rf test/e2e/log

CI := $(shell find . -iname "*.jsonnet") $(shell find . -iname "*.libsonnet")
$(CI):
	jsonnet fmt -i -n 4 $@

gen-ci: $(CI)
	ffctl gen

CODEGEN := ./vendor/k8s.io/code-generator/generate-groups.sh

$(CODEGEN):
	# dep doesn't currently support downloading dependencies that don't have go in the top-level dir.
	# can move to managing with dep when merged: https://github.com/golang/dep/pull/1545
	mkdir -p vendor/k8s.io/code-generator
	git clone --branch release-1.11 https://github.com/kubernetes/code-generator.git vendor/k8s.io/code-generator

pkg/package-server/generated/openapi/zz_generated.openapi.go:
	go run -mod=vendor vendor/k8s.io/kube-openapi/cmd/openapi-gen/openapi-gen.go --logtostderr -i ./vendor/k8s.io/apimachinery/pkg/runtime,./vendor/k8s.io/apimachinery/pkg/apis/meta/v1,./vendor/k8s.io/apimachinery/pkg/version,./pkg/package-server/apis/packagemanifest/v1alpha1 -p github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/generated/openapi/ -O zz_generated.openapi -h boilerplate.go.txt -r /dev/null
	
clean-openapi:
	rm -rf pkg/package-server/generated/openapi

codegen-openapi: clean-openapi pkg/package-server/generated/openapi/zz_generated.openapi.go

<<<<<<< HEAD
# our version of hack/update-codegen.sh
=======
# Must be run in gopath: https://github.com/kubernetes/kubernetes/issues/67566
# use container-codegen
>>>>>>> b9933db3... chore(dependencies): migrate to go modules
codegen: $(CODEGEN)
	$(CODEGEN) all $(PKG)/pkg/api/client $(PKG)/pkg/api/apis "operators:v1alpha1,v1alpha2"
	$(CODEGEN) all $(PKG)/pkg/package-server/client $(PKG)/pkg/package-server/apis "packagemanifest:v1alpha1"

container-codegen:
	docker build -t olm:codegen -f codegen.Dockerfile .
	docker run --name temp-codegen olm:codegen /bin/true
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/. ./pkg/api/client
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/. ./pkg/api/apis
	docker rm temp-codegen

verify-codegen: codegen
	git diff --exit-code

verify-catalog: schema-check
	go test -mod=vendor -v ./test/schema/catalog_versions_test.go

counterfeiter := $(GOBIN)/counterfeiter
$(counterfeiter):
	go install github.com/maxbrunsfeld/counterfeiter

mockgen := $(GOBIN)/mockgen
$(mockgen):
	go install github.com/golang/mock/mockgen

generate-mock-client: $(counterfeiter)
	go generate ./$(PKG_DIR)/...
	mockgen -source ./pkg/lib/operatorclient/client.go -destination ./pkg/lib/operatorclient/mock_client.go -package operatorclient

gen-all: gen-ci codegen generate-mock-client codegen-openapi

# before running release, bump the version in OLM_VERSION and push to master,
# then tag those builds in quay with the version in OLM_VERSION
release: ver=$(shell cat OLM_VERSION)
release:
	docker pull quay.io/coreos/olm:$(ver)
	$(MAKE) target=upstream ver=$(ver) package
	$(MAKE) target=okd ver=$(ver) package
	$(MAKE) target=ocp ver=$(ver) package
	rm -rf manifests
	mkdir manifests
	cp -R deploy/ocp/manifests/$(ver)/. manifests

package: olmref=$(shell docker inspect --format='{{index .RepoDigests 0}}' quay.io/coreos/olm:$(ver))
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
	./scripts/package-release.sh $(ver) deploy/$(target)/manifests/$(ver) deploy/$(target)/values.yaml
	ln -sfFn ./$(ver) deploy/$(target)/manifests/latest
