##########################
#  OLM - Build and Test  #
##########################

SHELL := /bin/bash
PKG   := github.com/operator-framework/operator-lifecycle-manager
MOD_FLAGS := $(shell (go version | grep -q -E "1\.(11|12)") && echo -mod=vendor)
CMDS  := $(shell go list $(MOD_FLAGS) ./cmd/...)
TCMDS := $(shell go list $(MOD_FLAGS) ./test/e2e/...)
CODEGEN_INTERNAL := ./vendor/k8s.io/code-generator/generate_internal_groups.sh
MOCKGEN := ./scripts/generate_mocks.sh
# counterfeiter := $(GOBIN)/counterfeiter
# mockgen := $(GOBIN)/mockgen
IMAGE_REPO := quay.io/operator-framework/olm
IMAGE_TAG ?= "dev"
SPECIFIC_UNIT_TEST := $(if $(TEST),-run $(TEST),)
LOCAL_NAMESPACE := "olm"
export GO111MODULE=on

# ART builds are performed in dist-git, with content (but not commits) copied 
# from the source repo. Thus at build time if your code is inspecting the local
# git repo it is getting unrelated commits and tags from the dist-git repo, 
# not the source repo.
# For ART image builds, SOURCE_GIT_COMMIT, SOURCE_GIT_TAG, SOURCE_DATE_EPOCH 
# variables are inserted in Dockerfile to enable recovering the original git 
# metadata at build time.
GIT_COMMIT := $(if $(SOURCE_GIT_COMMIT),$(SOURCE_GIT_COMMIT),$(shell git rev-parse HEAD))

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e .FORCE

all: test build

test: clean cover.out

unit:
	go test $(MOD_FLAGS) $(SPECIFIC_UNIT_TEST) -v -race -tags=json1 -count=1 ./pkg/...

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
	. ./scripts/package_release.sh 1.0.0 build/resources Documentation/install/local-values.yaml
	. ./scripts/install_local.sh $(LOCAL_NAMESPACE) build/resources
	rm -rf build


deploy-local:
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources Documentation/install/local-values.yaml
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

CI := $(shell find . -iname "*.jsonnet") $(shell find . -iname "*.libsonnet")
$(CI):
	jsonnet fmt -i -n 4 $@

gen-ci: $(CI)
	ffctl gen

# Must be run in gopath: https://github.com/kubernetes/kubernetes/issues/67566
# use container-codegen
codegen: export GO111MODULE := off
codegen:
	cp scripts/generate_internal_groups.sh vendor/k8s.io/code-generator/generate_internal_groups.sh
	mkdir -p vendor/k8s.io/code-generator/hack
	cp boilerplate.go.txt vendor/k8s.io/code-generator/hack/boilerplate.go.txt
	go run vendor/k8s.io/kube-openapi/cmd/openapi-gen/openapi-gen.go --logtostderr -i ./vendor/k8s.io/apimachinery/pkg/runtime,./vendor/k8s.io/apimachinery/pkg/apis/meta/v1,./vendor/k8s.io/apimachinery/pkg/version,./pkg/package-server/apis/operators/v1,./pkg/package-server/apis/apps/v1alpha1,./pkg/api/apis/operators/v1alpha1,./pkg/lib/version -p $(PKG)/pkg/package-server/apis/openapi -O zz_generated.openapi -h boilerplate.go.txt -r /dev/null
	$(CODEGEN_INTERNAL) deepcopy,conversion,client,lister,informer $(PKG)/pkg/api/client $(PKG)/pkg/api/apis $(PKG)/pkg/api/apis "operators:v1alpha1,v1"
	$(CODEGEN_INTERNAL) all $(PKG)/pkg/package-server/client $(PKG)/pkg/package-server/apis $(PKG)/pkg/package-server/apis "operators:v1 apps:v1alpha1"

container-codegen:
	docker build -t olm:codegen -f codegen.Dockerfile .
	docker run --name temp-codegen olm:codegen /bin/true
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/. ./pkg/api/client
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/. ./pkg/api/apis
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/. ./pkg/package-server/apis
	docker cp temp-codegen:/go/src/github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/client/. ./pkg/package-server/client
	docker rm temp-codegen

container-mockgen:
	docker build -t olm:mockgen -f mockgen.Dockerfile . --no-cache
	docker run --name temp-mockgen olm:mockgen /bin/true
	docker cp temp-mockgen:/operator-lifecycle-manager/pkg/api/wrappers/wrappersfakes/. ./pkg/api/wrappers/wrappersfakes
	docker cp temp-mockgen:/operator-lifecycle-manager/pkg/lib/operatorlister/operatorlisterfakes/. ./pkg/lib/operatorlister/operatorlisterfakes
	docker cp temp-mockgen:/operator-lifecycle-manager/pkg/lib/operatorclient/operatorclientmocks/. ./pkg/lib/operatorclient/operatorclientmocks
	docker cp temp-mockgen:/operator-lifecycle-manager/pkg/fakes/. ./pkg/fakes
	docker cp temp-mockgen:/operator-lifecycle-manager/pkg/controller/registry/resolver/fakes/. ./pkg/controller/registry/resolver/fakes
	docker cp temp-mockgen:/operator-lifecycle-manager/pkg/package-server/client/fakes/. ./pkg/package-server/client/fakes
	docker rm temp-mockgen

verify: verify-codegen verify-manifests

# Must be run in gopath: https://github.com/kubernetes/kubernetes/issues/67566
verify-codegen: codegen
	git diff --exit-code

# this is here for backwards compatibility with the ci job that calls verify-catalog
verify-catalog:

# this is here for backwards compatibility with the ci job that calls verify-manifests
verify-manifests:

verify-release: ver=$(shell cat OLM_VERSION)
verify-release:
	rm -rf manifests
	mkdir manifests
	./scripts/package_release.sh $(ver) manifests deploy/ocp/values.yaml
	# requires gnu sed if on mac
	find ./manifests -type f -exec sed -i "/^#/d" {} \;
	find ./manifests -type f -exec sed -i "1{/---/d}" {} \;
	git diff --exit-code

mockgen:
	$(MOCKGEN)

gen-all: gen-ci container-codegen container-mockgen

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
	# requires gnu sed if on mac
	find ./manifests -type f -exec sed -i "/^#/d" {} \;
	find ./manifests -type f -exec sed -i "1{/---/d}" {} \;

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
run-local: build-linux
	@echo Building OLM:
	rm -rf build
	. ./scripts/build_local.sh
	mkdir -p build/resources
	. ./scripts/package_release.sh 1.0.0 build/resources Documentation/install/local-values.yaml
	. ./scripts/install_local.sh local build/resources
	rm -rf build

.PHONY: install-crd
install-crd:
	@echo Applying upstream quick start crd ...
	- kubectl apply -f deploy/upstream/quickstart/crds.yaml
	- kubectl apply -f deploy/upstream/quickstart/olm.yaml

.PHONY: install-ocp
install-ocp:
	@echo Applying latest ...
	- kubectl create -f deploy/ocp/manifests/latest/
	@echo Adding cluster-admin permissions to system:serviceaccount:kube-system:default...
	- oc adm policy add-cluster-role-to-user cluster-admin system:serviceaccount:kube-system:default