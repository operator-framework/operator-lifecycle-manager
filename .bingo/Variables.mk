# Auto generated binary variables helper managed by https://github.com/bwplotka/bingo v0.9. DO NOT EDIT.
# All tools are designed to be build inside $GOBIN.
BINGO_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GOPATH ?= $(shell go env GOPATH)
GOBIN  ?= $(firstword $(subst :, ,${GOPATH}))/bin
GO     ?= $(shell which go)

# Below generated variables ensure that every time a tool under each variable is invoked, the correct version
# will be used; reinstalling only if needed.
# For example for helm variable:
#
# In your main Makefile (for non array binaries):
#
#include .bingo/Variables.mk # Assuming -dir was set to .bingo .
#
#command: $(HELM)
#	@echo "Running helm"
#	@$(HELM) <flags/args..>
#
HELM := $(GOBIN)/helm-v3.14.4
$(HELM): $(BINGO_DIR)/helm.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/helm-v3.14.4"
	@cd $(BINGO_DIR) && GOWORK=off $(GO) build -mod=mod -modfile=helm.mod -o=$(GOBIN)/helm-v3.14.4 "helm.sh/helm/v3/cmd/helm"

KIND := $(GOBIN)/kind-v0.22.0
$(KIND): $(BINGO_DIR)/kind.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/kind-v0.22.0"
	@cd $(BINGO_DIR) && GOWORK=off $(GO) build -mod=mod -modfile=kind.mod -o=$(GOBIN)/kind-v0.22.0 "sigs.k8s.io/kind"

SETUP_ENVTEST := $(GOBIN)/setup-envtest-v0.0.0-20240507051437-479b723944e3
$(SETUP_ENVTEST): $(BINGO_DIR)/setup-envtest.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/setup-envtest-v0.0.0-20240507051437-479b723944e3"
	@cd $(BINGO_DIR) && GOWORK=off $(GO) build -mod=mod -modfile=setup-envtest.mod -o=$(GOBIN)/setup-envtest-v0.0.0-20240507051437-479b723944e3 "sigs.k8s.io/controller-runtime/tools/setup-envtest"

YQ_V3 := $(GOBIN)/yq_v3-v3.0.0-20201202084205-8846255d1c37
$(YQ_V3): $(BINGO_DIR)/yq_v3.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/yq_v3-v3.0.0-20201202084205-8846255d1c37"
	@cd $(BINGO_DIR) && GOWORK=off $(GO) build -mod=mod -modfile=yq_v3.mod -o=$(GOBIN)/yq_v3-v3.0.0-20201202084205-8846255d1c37 "github.com/mikefarah/yq/v3"

