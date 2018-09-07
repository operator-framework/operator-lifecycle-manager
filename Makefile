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

unit:
	go test -v -race ./pkg/...

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
	@if [ cover-$(GENCOVER) = cover-true ]; then \
		echo "building bin/$(shell basename $@)" with coverage; \
		GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -o $@ -c -covermode=count -coverpkg ./pkg/controller/... $(PKG)/cmd/$(shell basename $@); \
	else \
		echo "building bin/$(shell basename $@)"; \
		go build -ldflags "-w -X $(PKG)/pkg/version.GitCommit=`git rev-parse --short HEAD` -X $(PKG)/pkg/version.OLMVersion=`cat OLM_VERSION`" \
		-o $@ $(PKG)/cmd/$(shell basename $@); \
	fi

run-local:
	. ./scripts/build_local.sh
	mkdir -p build/resources
	. ./scripts/package-release.sh 1.0.0-local build/resources Documentation/install/local-values.yaml
	. ./scripts/install_local.sh local build/resources

run-local-shift:
	. ./scripts/build_local_shift.sh
	mkdir -p build/resources
	. ./scripts/package-release.sh 1.0.0-local build/resources Documentation/install/local-values-shift.yaml
	. ./scripts/install_local.sh local build/resources

e2e-local:
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_local.sh $(TEST)

e2e-local-shift:
	. ./scripts/build_local_shift.sh
	. ./scripts/run_e2e_local.sh $(TEST)

e2e-local-docker:
	. ./scripts/build_local.sh
	. ./scripts/run_e2e_docker.sh $(TEST)

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

codegen: $(CODEGEN)
	$(CODEGEN) all $(PKG)/pkg/api/client $(PKG)/pkg/api/apis "operators:v1alpha1"

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

# must have already tagged a version release in github so that the docker images are available
# make ver=0.3.0 release
release:
ifndef ver
	$(error ver is undefined)
endif
	docker pull quay.io/coreos/olm:$(ver)
	docker pull quay.io/coreos/catalog:$(ver)
	yaml w -i deploy/upstream/values.yaml alm.image.ref `docker inspect --format='{{index .RepoDigests 0}}' quay.io/coreos/olm:$(ver)`
	yaml w -i deploy/upstream/values.yaml catalog.image.ref `docker inspect --format='{{index .RepoDigests 0}}' quay.io/coreos/catalog:$(ver)`
	./scripts/package-release.sh $(ver) deploy/upstream/manifests/$(ver) deploy/upstream/values.yaml
