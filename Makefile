##########################
#  OLM - Build and Test  #
##########################

SHELL := /bin/bash
PKG   := github.com/operator-framework/operator-lifecycle-manager
CMDS  := $(addprefix bin/, $(shell go list ./cmd/... | xargs -I{} basename {}))
CODEGEN := ./vendor/k8s.io/code-generator/generate_groups.sh
MOCKGEN := ./scripts/generate_mocks.sh
counterfeiter := $(GOBIN)/counterfeiter
mockgen := $(GOBIN)/mockgen
IMAGE_REPO := quay.io/coreos/olm
IMAGE_TAG ?= "dev"
KUBE_DEPS := api apiextensions-apiserver apimachinery code-generator kube-aggregator kubernetes
KUBE_RELEASE := release-1.11
MOD_FLAGS := $(shell (go version | grep -q 1.11) && echo -mod=vendor)

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e .FORCE

all: test build

test: clean cover.out

unit:
	go test $(MOD_FLAGS) -v -race ./pkg/...

schema-check:
	go run $(MOD_FLAGS) ./cmd/validator/main.go ./deploy/chart/catalog_resources

cover.out: schema-check
	go test $(MOD_FLAGS) -v -race -coverprofile=cover.out -covermode=atomic \
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

$(CMDS): version_flags=-ldflags "-w -X $(PKG)/pkg/version.GitCommit=`git rev-parse --short HEAD` -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`"
$(CMDS):
	CGO_ENABLED=0 go $(build_cmd) $(MOD_FLAGS) $(version_flags) -o $@ $(PKG)/cmd/$(shell basename $@);

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

# kube dependencies all should be at the same release and should match up with client go
# go.mod currently doesn't support specifying a branch name to track, and kube isn't publishing good version tags
$(KUBE_DEPS):
	go get -m k8s.io/$@@$(KUBE_RELEASE)

vendor: $(KUBE_DEPS)
	go mod tidy
	go mod vendor

container:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

clean:
	@rm -rf cover.out
	@rm -rf bin
	@rm -rf test/e2e/resources
	@rm -rf test/e2e/test-resources
	@rm -rf test/e2e/log

CI := $(shell find . -iname "*.jsonnet") $(shell find . -iname "*.libsonnet")
$(CI):
	jsonnet fmt -i -n 4 $@

gen-ci: $(CI)
	ffctl gen

# Must be run in gopath: https://github.com/kubernetes/kubernetes/issues/67566
# use container-codegen
codegen:
	cp scripts/generate_groups.sh vendor/k8s.io/code-generator/generate_groups.sh
	mkdir -p vendor/k8s.io/code-generator/hack
	cp boilerplate.go.txt vendor/k8s.io/code-generator/hack/boilerplate.go.txt
	go run vendor/k8s.io/kube-openapi/cmd/openapi-gen/openapi-gen.go --logtostderr -i ./vendor/k8s.io/apimachinery/pkg/runtime,./vendor/k8s.io/apimachinery/pkg/apis/meta/v1,./vendor/k8s.io/apimachinery/pkg/version,./pkg/package-server/apis/packagemanifest/v1alpha1 -p $(PKG)/pkg/package-server/apis/openapi -O zz_generated.openapi -h boilerplate.go.txt -r /dev/null
	$(CODEGEN) all $(PKG)/pkg/api/client $(PKG)/pkg/api/apis "operators:v1alpha1,v1alpha2"
	$(CODEGEN) all $(PKG)/pkg/package-server/client $(PKG)/pkg/package-server/apis "packagemanifest:v1alpha1"

container-codegen:
	docker build -t olm:codegen -f codegen.Dockerfile .
	docker run --name temp-codegen olm:codegen /bin/true
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/. ./pkg/api/client
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/. ./pkg/api/apis
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/. ./pkg/package-server/apis
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/. ./pkg/package-server/client
	docker rm temp-codegen

container-mockgen:
	docker build -t olm:mockgen -f mockgen.Dockerfile .
	docker run --name temp-mockgen olm:mockgen /bin/true
	docker cp temp-mockgen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/wrappers/wrappersfakes/. ./pkg/api/wrappers/wrappersfakes
	docker cp temp-mockgen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/fakes/client-go/listers/. ./pkg/fakes/client-go/listers
	docker cp temp-mockgen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes/. ./pkg/lib/operatorlister/operatorlisterfakes
	docker cp temp-mockgen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/lib/operatorclient/mock_client.go ./pkg/lib/operatorclient/mock_client.go
	docker rm temp-mockgen

# Must be run in gopath: https://github.com/kubernetes/kubernetes/issues/67566
verify-codegen: codegen
	git diff --exit-code

verify-catalog: schema-check
	go test $(MOD_FLAGS) -v ./test/schema/catalog_versions_test.go

generate-mock-client: 
	$(MOCKGEN)

gen-all: gen-ci container-codegen container-mockgen

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
