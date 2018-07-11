##########################
#  OLM - Build and Test  #
##########################

SHELL := /bin/bash
PKG   := github.com/operator-framework/operator-lifecycle-manager
CMDS  := $(addprefix bin/, $(shell go list ./cmd/... | xargs -I{} basename {}))
IMAGE_REPO := quay.io/coreos/olm
IMAGE_TAG ?= "dev"

.FORCE:

.PHONY: build test run clean vendor schema-check \
	vendor-update coverage coverage-html e2e .FORCE

all: test build

test: schema-check cover.out

schema-check:
	go run ./cmd/validator/main.go ./deploy/chart/catalog_resources

cover.out: schema-check
	go test -v -race -coverprofile=cover.out -covermode=atomic \
		-coverpkg ./pkg/controller/... ./pkg/...

coverage: cover.out
	go tool cover -func=cover.out

coverage-html: cover.out
	go tool cover -html=cover.out

build: $(CMDS)

# build versions of the binaries with coverage enabled
build-coverage: GENCOVER=true
build-coverage: $(CMDS)

$(CMDS): .FORCE
	if [ 1$(GENCOVER) = 1true ]; then \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -o $@ -c -covermode=count -coverpkg ./pkg/... $(PKG)/cmd/$(shell basename $@); \
	else \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $@ $(PKG)/cmd/$(shell basename $@); \
	fi

build/chart/values.yaml: $(values_file)
	mkdir -p build/chart
	cp $(values_file) build/chart/values.yaml

build/chart/Chart.yaml: deploy/chart/Chart.yaml
	mkdir -p build/chart
	cp deploy/chart/Chart.yaml build/chart/Chart.yaml
	echo "version: ver=1.0.0-local" >> build/chart/Chart.yaml

RESOURCES:=$(shell ls deploy/chart/templates/*yaml |  xargs -I{} basename {})
CHARTS:=$(addprefix build/chart/templates/,$(RESOURCES))
build/chart/templates/%.yaml: deploy/chart/templates/%.yaml
	mkdir -p build/chart/templates
	cp $< $@

manifests: clean $(CHARTS) build/chart/Chart.yaml build/chart/values.yaml
	mkdir -p build/resources
	helm template -n olm -f build/chart/values.yaml build/chart --output-dir build/resources

run-local: values_file = Documentation/install/local-values.yaml
run-local: ver=0.0.0-local
run-local: manifests
	. ./scripts/build_local.sh
	. ./scripts/install_local.sh local build/resources/olm/templates

run-local-shift: values_file = Documentation/install/local-values-shift.yaml
run-local-shift: ver=0.0.0-local
run-local-shift: manifests
	. ./scripts/build_local_shift.sh
	. ./scripts/install_local.sh local build/resources/olm/templates

e2e-local: values_file = test/e2e/e2e-values.yaml
e2e-local: manifests
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_local.sh

e2e-local-shift: values_file = test/e2e/e2e-values.yaml
e2e-local-shift: manifests
	. ./scripts/build_local_shift.sh
	. ./scripts/run_e2e_local.sh

e2e-local-docker: values_file = test/e2e/e2e-values.yaml
e2e-local-docker: manifests
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_docker.sh

DEP := $(GOPATH)/bin/dep
$(DEP):
	go get -u github.com/golang/dep/cmd/dep

vendor: $(DEP)
	$(DEP) ensure -v -vendor-only

vendor-update: $(DEP)
	$(DEP) ensure -v

container: build
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

clean:
	rm -rf bin
	rm -rf test/e2e/test-resources
	rm -rf test/e2e/log
	rm -rf build

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
	git clone --branch release-1.9 https://github.com/kubernetes/code-generator.git vendor/k8s.io/code-generator
	# codegen tools currently don't allow specifying custom boilerplate, so we move ours to the default location
	mkdir -p $(GOPATH)/src/k8s.io/kubernetes/hack/boilerplate
	cp boilerplate.go.txt $(GOPATH)/src/k8s.io/kubernetes/hack/boilerplate/boilerplate.go.txt

define replace
@find ./pkg/api/client -type f -exec \
		sed -i.bak 's/\(\"'$(1)'\)\(-v1\)*\(s\)*/\1-v1\3/g' {} \; -exec rm {}.bak \;
@find ./pkg/api/client -type f -exec \
		sed -i.bak 's/Group: \"'$(1)'-v1\"/Group: \"app.coreos.com\"/g' {} \; -exec rm {}.bak \;
endef
codegen: $(CODEGEN)
	$(CODEGEN) all $(PKG)/pkg/api/client $(PKG)/pkg/api/apis \
		"catalogsource:v1alpha1 clusterserviceversion:v1alpha1 installplan:v1alpha1 subscription:v1alpha1"
	# codegen doesn't respect pluralnames, so we manually set them here
	$(call replace,"catalogsource")
	$(call replace,"clusterserviceversion")
	$(call replace,"installplan")
	$(call replace,"subscription")

verify-codegen: codegen
	git diff --exit-code

verify-catalog: schema-check
	go test -v ./test/schema/catalog_versions_test.go

counterfeiter := $(GOBIN)/counterfeiter
$(counterfeiter):
	go install github.com/maxbrunsfeld/counterfeiter

mockgen := $(GOBIN)/mockgen
$(mockgen):
	go install github.com/golang/mock/mockgen


generate-mock-client: $(counterfeiter)
	go generate ./$(PKG_DIR)/...
	mockgen -source ./pkg/lib/operatorclient/client.go -destination ./pkg/lib/operatorclient/mock_client.go -package operatorclient

gen-all: gen-ci codegen generate-mock-client

# make ver=0.3.0 tectonic-release
tectonic-release:
	./scripts/package-release.sh $(ver) deploy/tectonic-alm-operator/manifests/$(ver) deploy/tectonic-alm-operator/values.yaml

# make ver=0.3.0 upstream-release
upstream-release:
	./scripts/package-release.sh $(ver) deploy/upstream/manifests/$(ver) deploy/upstream/values.yaml


YQ := $(GOPATH)/bin/yq
$(YQ):
	go get -u github.com/mikefarah/yq

# make ver=0.3.0 ansible-release
ansible-release: $(YQ)
	# ansible release uses openshift-ansible submodule
	git submodule init
	git submodule update
	# copy base role to versioned release
	mkdir -p deploy/aos-olm/$(ver)
	cp -R deploy/role/. deploy/aos-olm/$(ver)/
	# copy manifest files into release
	./scripts/package-release.sh $(ver) deploy/aos-olm/$(ver)/files deploy/aos-olm/values.yaml
	# generate install/remove tasks based on manifest files
	./scripts/k8s_yaml_to_ansible_install.sh deploy/aos-olm/$(ver)/files deploy/aos-olm/$(ver)/tasks/install.yaml
	./scripts/k8s_yaml_to_ansible_remove.sh deploy/aos-olm/$(ver)/files deploy/aos-olm/$(ver)/tasks/remove_components.yaml
	# link newest release into playbook
	ln -sfF ../../../../deploy/aos-olm/$(ver) deploy/aos-olm/playbook/private/roles/olm

OLM_REF:=$(shell docker inspect --format='{{index .RepoDigests 0}}' quay.io/coreos/olm:$(ver))
CATALOG_REF:=$(shell docker inspect --format='{{index .RepoDigests 0}}' quay.io/coreos/catalog:$(ver))

# make ver=0.3.0 release
# must have already tagged a version release in github so that the docker images are available
release:
ifndef ver
  $(error ver is undefined)
endif
	docker pull quay.io/coreos/olm:$(ver) 
	docker pull quay.io/coreos/catalog:$(ver)
	yaml w -i deploy/upstream/values.yaml alm.image.ref $(OLM_REF)
	yaml w -i deploy/upstream/values.yaml catalog.image.ref $(CATALOG_REF)
	yaml w -i deploy/tectonic-alm-operator/values.yaml alm.image.ref $(OLM_REF)
	yaml w -i deploy/tectonic-alm-operator/values.yaml catalog.image.ref $(CATALOG_REF)
	yaml w -i deploy/aos-olm/values.yaml alm.image.ref $(OLM_REF)
	yaml w -i deploy/aos-olm/values.yaml catalog.image.ref $(CATALOG_REF)
	$(MAKE) tectonic-release upstream-release ansible-release 