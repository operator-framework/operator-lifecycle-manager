# WIP

GO := GO111MODULE=on GOFLAGS="-mod=vendor" go
CMDS := $(shell $(GO) list ./cmd/... ./util/cpb ./test/e2e/wait)
GINKGO := $(GO) run github.com/onsi/ginkgo/ginkgo
BINDATA := $(GO) run github.com/go-bindata/go-bindata/v3/go-bindata

bin/wait: FORCE
	$(GO) build -o $@ ./test/e2e/wait

bin/cpb: FORCE
	CGO_ENABLED=0 $(GO) build -o $@ -ldflags '-extldflags "-static"' ./util/cpb

$(CMDS): FORCE
	$(GO) build -o bin/$(notdir $@) -ldflags "-X $(PKG)/pkg/version.GitCommit=$(GIT_COMMIT) -X $(PKG)/pkg/version.OLMVersion=$(OLM_VERSION)" $@

test/e2e/assets/chart/zz_chart.go: $(shell find deploy/chart -type f)
	$(BINDATA) -o $@ -pkg chart -prefix deploy/chart/ $^

bin/e2e-local.test: FORCE test/e2e/assets/chart/zz_chart.go
	$(GO) test -c -tags kind,helm -o $@ ./test/e2e

bin/e2e-local.image.tar: export GOOS=linux
bin/e2e-local.image.tar: export GOARCH=386
bin/e2e-local.image.tar: e2e.Dockerfile bin/wait bin/cpb $(CMDS)
	docker build -t quay.io/operator-framework/olm:local -f $< bin
	docker save -o $@ quay.io/operator-framework/olm:local

.PHONY: e2e-local
e2e-local: bin/e2e-local.test bin/e2e-local.image.tar
	$(GINKGO) -nodes $(or $(NODES),1) -flakeAttempts 3 -randomizeAllSpecs $(if $(TEST),-focus "$(TEST)") -v -timeout 90m $< -- -namespace=operators -olmNamespace=operator-lifecycle-manager -dummyImage=bitnami/nginx:latest -kind.images=e2e-local.image.tar

# Phony prerequisite for targets that rely on the go build cache to
# determine staleness.
.PHONY: FORCE
FORCE:
