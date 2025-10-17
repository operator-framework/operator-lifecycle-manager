# AGENTS.md

This file provides guidance to AI agents when working with code in this repository.

## Project Overview

This is the Operator Lifecycle Manager (OLM) v0 repository, which is currently in **maintenance mode**. This project provides declarative installation, upgrade, and dependency management for Kubernetes operators through custom resources and controllers.

**Important Note**: This is OLM v0 in maintenance mode. For new development, prefer [operator-controller](https://github.com/operator-framework/operator-controller) (OLM v1). Only critical bug fixes and security updates are accepted here.

## Essential Commands for AI Agents

### Build and Development
```bash
make build                  # Build binaries for local OS/ARCH
make build-utils           # Build utility binaries
make image                 # Build container image for linux
make local-build           # Build image with 'local' tag
make e2e-build             # Build image for e2e testing
```

### Testing and Validation
```bash
make test                  # Run all tests (unit + test-split)
make unit                  # Run unit tests with setup-envtest
make test-split            # Run e2e test split utility tests
make coverage              # Run unit tests with coverage
make e2e                   # Run e2e tests against existing cluster
make e2e-local             # Build + deploy + run e2e tests locally
```

### Code Quality and Generation
```bash
make lint                  # Run golangci-lint
make vet                   # Run go vet
make fmt                   # Run go fmt
make verify                # Run all verification checks
make gen-all               # Update API, generate code and mocks
make codegen               # Generate clients, deepcopy, listers, informers
make mockgen               # Generate mocks
make manifests             # Copy OLM API CRD manifests
```

### Local Development Environment
```bash
make kind-create           # Create kind cluster (kind-olmv0)
make kind-clean            # Delete kind cluster
make cert-manager-install  # Install cert-manager
make deploy                # Deploy OLM to kind cluster
make undeploy              # Uninstall OLM from kind cluster
make run-local             # Full local setup: build + kind + cert-manager + deploy
```

### Dependency Management
```bash
make vendor                # Update vendored dependencies
make bingo-upgrade         # Upgrade tools managed by bingo
```

## Architecture Overview for AI Agents

OLM consists of two main operators managing different aspects of the operator lifecycle:

### OLM Operator
- Manages **ClusterServiceVersions (CSV)** - the actual operator installation
- Creates deployments, service accounts, RBAC resources
- Handles the CSV lifecycle: Pending → InstallReady → Installing → Succeeded/Failed

### Catalog Operator  
- Manages **InstallPlans** - dependency resolution and installation plans
- Manages **Subscriptions** - automatic updates from catalog channels
- Manages **CatalogSources** - repositories of operators
- Handles dependency resolution between operators

### Core Resources Reference
| Resource | Short | Owner | Purpose |
|----------|-------|-------|---------|
| ClusterServiceVersion | csv | OLM | Operator metadata and installation strategy |
| InstallPlan | ip | Catalog | Resolved list of resources to install/upgrade |
| CatalogSource | catsrc | Catalog | Repository of operators and metadata |
| Subscription | sub | Catalog | Tracks operator updates from catalog channels |
| OperatorGroup | og | OLM | Groups namespaces for operator installation scope |

## AI Agent Development Guidelines

### Tool Management
- **bingo** (`.bingo/Variables.mk`) - Manages development tools like golangci-lint, helm, kind, setup-envtest
- **tools.go** - Manages code generation tools and shared dependencies with main module
- All tools are version-pinned for reproducible builds

### Code Generation Patterns
Most code is generated rather than hand-written:
- Client libraries from CRDs using k8s.io/code-generator
- Mocks using counterfeiter and gomock  
- CRD manifests copied from operator-framework/api
- Deep-copy methods and informers

### Testing Strategy for AI Agents
- **Unit tests**: Use setup-envtest for real Kubernetes API behavior
- **E2E tests**: Full cluster testing with Ginkgo/Gomega using kind
- **Race detection**: Enabled by default with CGO_ENABLED=1
- **Bundle/catalog testing**: Custom test images in test/images/

### Build Configuration
- **Vendor mode**: All dependencies vendored for reproducible builds
- **Build tags**: `e2e` and `experimental_metrics` for specific test builds
- **Trimmed paths**: Build-time path trimming for smaller binaries
- **Version injection**: Git commit and version info embedded at build time

## Key Dependencies

- **Kubernetes**: v0.34.1 (tracks specific k8s minor version)
- **operator-framework/api**: v0.35.0 (OLM API definitions)
- **operator-registry**: v1.60.0 (catalog/bundle tooling)
- **controller-runtime**: v0.22.2 (Kubernetes controller framework)
- **Ginkgo/Gomega**: v2.26.0/v1.38.2 (BDD testing framework)

## AI Agent Constraints and Guidelines

### Project Status Constraints
- **Maintenance mode only** - no new features accepted
- Only critical security fixes and outage issues addressed
- Direct users to [operator-controller](https://github.com/operator-framework/operator-controller) for OLM v1 development

### Development Requirements
- Go 1.24+
- Docker/Podman for container builds
- kind for local Kubernetes clusters
- kubectl for cluster interaction

### Testing Environment Configuration
- Uses envtest with Kubernetes v0.34.x binaries
- Default kind cluster name: `kind-olmv0`
- E2E timeout: 90 minutes (configurable via E2E_TIMEOUT)
- Test images hosted on quay.io/olmtest organization

## AI Agent Best Practices

1. **Always run `make verify` before suggesting changes**
2. **Use generated code patterns** - most code is auto-generated
3. **Test with real Kubernetes APIs** using setup-envtest
4. **Follow maintenance mode restrictions** - only critical fixes
5. **Reference OLM v1 for new development** - this is legacy maintenance