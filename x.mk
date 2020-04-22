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
	$(GINKGO) $(if $(FOCUS),-focus "$(FOCUS)" )-nodes $(or $(NODES),1) -randomizeAllSpecs -v -timeout 70m $< -- -namespace=operators -olmNamespace=operator-lifecycle-manager -dummyImage=bitnami/nginx:latest -kind.images=e2e-local.image.tar

.PHONY: e2e-ci
e2e-ci: NODES=$(or $(E2E_CI_NODES),1)
e2e-ci: FOCUS=$(shell $(GINKGO) -seed $(or $(E2E_CI_SEED),0) -randomizeAllSpecs -noColor -failOnPending -succinct -v -dryRun bin/e2e-local.test | grep -C4 '^-' | awk 'function trimmed(){return gensub(/^\s*(\S.*\S)\s*$$/,"\\1","g")} NR%5==1{printf "%s ",trimmed()} NR%5==2{print trimmed()}' | sed -e 's/[^ a-zA-Z0-9]/./g' | awk "NR%$(or $(E2E_CI_JOBS),1)==$(or $(E2E_CI_JOB),0){printf \"%s|\",\$$0}" | sed -e 's/|$$//' )
e2e-ci: e2e-local

# Phony prerequisite for targets that rely on the go build cache to
# determine staleness.
.PHONY: FORCE
FORCE:
