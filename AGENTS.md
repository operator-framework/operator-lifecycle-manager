# AGENTS.md

This file provides AI agents with comprehensive context about the Operator Lifecycle Manager (OLM) v0 codebase to enable effective navigation, understanding, and contribution.

## Project Status

**CRITICAL**: This repository is in **maintenance mode**. OLM v0 accepts only critical bug fixes and security updates. For new development, use [operator-controller](https://github.com/operator-framework/operator-controller) (OLM v1).

## Project Overview

Operator Lifecycle Manager (OLM) extends Kubernetes to provide declarative installation, upgrade, and lifecycle management for Kubernetes operators. It's part of the [Operator Framework](https://github.com/operator-framework) ecosystem.

### Core Capabilities
- **Over-the-Air Updates**: Automatic operator updates via catalog channels
- **Dependency Resolution**: Automatic resolution and installation of operator dependencies
- **Multi-tenancy**: Namespace-scoped operator management via OperatorGroups
- **Discovery**: Catalog-based operator discovery and installation
- **Stability**: Prevents conflicting operators from owning the same APIs

## Architecture

OLM consists of two main operators working together:

### 1. OLM Operator (`cmd/olm`)
**Responsibility**: Manages the installation and lifecycle of operators defined by ClusterServiceVersions (CSVs)

**Key Functions**:
- Creates Deployments, ServiceAccounts, Roles, and RoleBindings from CSV specifications
- Manages CSV lifecycle states: None → Pending → InstallReady → Installing → Succeeded/Failed
- Monitors installed operator health and rotates certificates
- Enforces OperatorGroup namespace scoping

**Primary Controllers**:
- CSV Controller (pkg/controller/operators/olm)
- OperatorGroup Controller

### 2. Catalog Operator (`cmd/catalog`)
**Responsibility**: Manages operator catalogs, subscriptions, and dependency resolution

**Key Functions**:
- Monitors CatalogSources and builds operator catalogs
- Processes Subscriptions to track operator updates
- Generates InstallPlans with resolved dependencies
- Creates CRDs and CSVs from catalog content

**Primary Controllers**:
- Subscription Controller
- InstallPlan Controller
- CatalogSource Controller
- Registry Reconciler

## Custom Resource Definitions (CRDs)

| Resource | API Group | Owner | Description |
|----------|-----------|-------|-------------|
| **ClusterServiceVersion (CSV)** | operators.coreos.com/v1alpha1 | OLM | Defines operator metadata, installation strategy, permissions, and owned/required CRDs |
| **Subscription** | operators.coreos.com/v1alpha1 | Catalog | Tracks operator updates from a catalog channel; drives automatic upgrades |
| **InstallPlan** | operators.coreos.com/v1alpha1 | Catalog | Calculated list of resources to install/upgrade; requires approval (manual or automatic) |
| **CatalogSource** | operators.coreos.com/v1alpha1 | Catalog | Repository of operators and metadata; served via grpc from operator-registry |
| **OperatorGroup** | operators.coreos.com/v1 | OLM | Groups namespaces for operator installation scope; enables multi-tenancy |
| **OperatorCondition** | operators.coreos.com/v2 | OLM | Tracks operator health status and conditions |

## Directory Structure

```
operator-lifecycle-manager/
├── cmd/                          # Entry point binaries
│   ├── catalog/                  # Catalog Operator main
│   ├── olm/                      # OLM Operator main
│   ├── package-server/           # Package API server
│   └── copy-content/             # Content copy utility
│
├── pkg/                          # Core implementation
│   ├── api/                      # API client and wrappers
│   │   ├── client/               # Generated Kubernetes clients
│   │   └── wrappers/             # Client wrapper utilities
│   │
│   ├── controller/               # Main controllers
│   │   ├── bundle/               # Bundle lifecycle controller
│   │   ├── install/              # Installation controller
│   │   ├── operators/            # Operator/CSV controllers (OLM Operator)
│   │   └── registry/             # Catalog/registry controllers (Catalog Operator)
│   │
│   ├── lib/                      # Shared libraries and utilities
│   │   ├── catalogsource/        # CatalogSource utilities
│   │   ├── csv/                  # CSV manipulation utilities
│   │   ├── operatorclient/       # Operator client abstractions
│   │   ├── operatorlister/       # Informer-based listers
│   │   ├── operatorstatus/       # Status management
│   │   ├── ownerutil/            # Owner reference utilities
│   │   ├── queueinformer/        # Queue-based informers
│   │   ├── scoped/               # Scoped client for multi-tenancy
│   │   └── [other utilities]
│   │
│   ├── metrics/                  # Prometheus metrics
│   └── package-server/           # Package server implementation
│
├── test/                         # Testing infrastructure
│   ├── e2e/                      # End-to-end tests
│   └── images/                   # Test container images
│
├── doc/                          # Documentation
│   ├── design/                   # Architecture and design docs
│   └── contributors/             # Contributor guides
│
└── vendor/                       # Vendored dependencies
```

## Key Packages and Their Responsibilities

### Controllers (`pkg/controller/`)

#### `pkg/controller/operators/`
The heart of the OLM Operator. Contains the CSV controller that manages the complete operator lifecycle.

**Key files**:
- `olm/operator.go` - Main OLM operator reconciler
- `olm/csv.go` - CSV reconciliation logic
- `catalog/operator.go` - Catalog operator reconciler

#### `pkg/controller/registry/`
Registry and catalog management for the Catalog Operator.

**Key files**:
- `reconciler/reconciler.go` - CatalogSource reconciliation
- `grpc/source.go` - gRPC catalog source handling

#### `pkg/controller/install/`
Manages the installation of resources defined in InstallPlans.

### Libraries (`pkg/lib/`)

#### `pkg/lib/operatorclient/`
Abstraction layer over Kubernetes clients providing OLM-specific operations.

#### `pkg/lib/operatorlister/`
Informer-based listers for efficient caching and querying of OLM resources.

#### `pkg/lib/queueinformer/`
Queue-based informer pattern used throughout OLM controllers for event-driven reconciliation.

#### `pkg/lib/ownerutil/`
Owner reference management ensuring proper garbage collection of OLM-managed resources.

#### `pkg/lib/scoped/`
Scoped clients that respect OperatorGroup namespace boundaries for multi-tenancy.

## Development Workflow

### Building
```bash
make build              # Build all binaries
make image              # Build container image
make local-build        # Build with 'local' tag
```

### Testing
```bash
make unit               # Unit tests with setup-envtest
make e2e                # E2E tests (requires cluster)
make e2e-local          # Build + deploy + e2e locally
make coverage           # Unit tests with coverage
```

### Code Generation
```bash
make gen-all            # Generate all code (clients, mocks, manifests)
make codegen            # Generate K8s clients and deep-copy methods
make mockgen            # Generate test mocks
make manifests          # Copy CRD manifests from operator-framework/api
```

### Local Development
```bash
make run-local          # Complete local setup
# OR step-by-step:
make kind-create        # Create kind cluster (kind-olmv0)
make cert-manager-install
make deploy             # Deploy OLM to cluster
```

## Key Design Patterns

### Control Loop State Machines

**CSV Lifecycle**:
```
None → Pending → InstallReady → Installing → Succeeded
                     ↑                          ↓
                     ←----------←------←-------Failed
```

**InstallPlan Lifecycle**:
```
None → Planning → RequiresApproval → Installing → Complete
                         ↓                ↓
                         ←-------←-------Failed
```

**Subscription Lifecycle**:
```
None → UpgradeAvailable → UpgradePending → AtLatestKnown
         ↑                                      ↓
         ←-----------←-----------←-------------←
```

### Dependency Resolution
- CSVs declare owned CRDs (what they provide) and required CRDs (what they need)
- Catalog Operator resolves transitive dependencies via graph traversal
- InstallPlans capture the complete dependency closure
- Resolution is based on (Group, Version, Kind) - no version pinning

### Catalog and Channel Model
```
Package (e.g., "etcd")
  ├── Channel: "stable" → CSV v0.9.4 → CSV v0.9.3 → CSV v0.9.2
  ├── Channel: "alpha"  → CSV v0.10.0
  └── Channel: "beta"   → CSV v0.9.4
```

Subscriptions track a channel; OLM follows the replacement chain to upgrade operators.

## Testing Strategy

### Unit Tests
- Use `setup-envtest` for real Kubernetes API behavior
- Race detection enabled by default (`CGO_ENABLED=1`)
- Mock generation via `counterfeiter` and `gomock`

### E2E Tests
- Full cluster testing with Ginkgo/Gomega BDD framework
- Test images in `test/images/` hosted on quay.io/olmtest
- Default timeout: 90 minutes (configurable via `E2E_TIMEOUT`)
- Uses kind cluster named `kind-olmv0`

### Integration Tests
- Bundle and catalog integration testing
- Upgrade path validation
- Multi-tenant scenario testing

## Important Dependencies

| Dependency | Version | Purpose |
|------------|---------|---------|
| kubernetes | v0.34.1 | Core K8s libraries |
| controller-runtime | v0.22.2 | Controller framework |
| operator-framework/api | v0.35.0 | OLM API definitions |
| operator-registry | v1.60.0 | Catalog/bundle tooling |
| ginkgo/gomega | v2.26.0 / v1.38.2 | BDD testing |

## Common Tasks for AI Agents

### Understanding Operator Installation Flow
1. User creates Subscription pointing to catalog/package/channel
2. Catalog Operator queries catalog for latest CSV in channel
3. Catalog Operator creates InstallPlan with resolved dependencies
4. Upon approval, Catalog Operator creates CRDs and CSV
5. OLM Operator detects CSV, validates requirements, creates Deployment/RBAC
6. CSV transitions through: Pending → InstallReady → Installing → Succeeded

### Debugging Installation Issues
- Check CSV status and conditions: `kubectl get csv -o yaml`
- Examine InstallPlan: `kubectl get ip -o yaml`
- Review operator logs: OLM Operator and Catalog Operator pods in `olm` namespace
- Verify OperatorGroup configuration for namespace scoping

### Adding New Functionality
**REMINDER**: This repository is in maintenance mode - only critical fixes accepted!

For understanding existing code:
1. Controllers follow controller-runtime patterns with Reconcile() methods
2. Use informers and listers from `pkg/lib/operatorlister`
3. Queue-based event handling via `pkg/lib/queueinformer`
4. Always respect OperatorGroup namespace scoping

### Code Generation
Most code is generated - don't hand-edit:
- Client code: Generated from CRDs using k8s.io/code-generator
- Deep-copy methods: Auto-generated for all API types
- Mocks: Generated via counterfeiter/gomock
- CRD manifests: Copied from operator-framework/api repository

Always run `make gen-all` after API changes.

## Navigation Tips

### Finding Controllers
- OLM Operator controllers: `pkg/controller/operators/`
- Catalog Operator controllers: `pkg/controller/registry/`, subscription/installplan logic in `pkg/controller/operators/catalog/`

### Finding API Definitions
- CRDs are defined in operator-framework/api (external dependency)
- Clients are in `pkg/api/client/`
- Listers are in `pkg/lib/operatorlister/`

### Finding Business Logic
- CSV installation: `pkg/controller/operators/olm/`
- Dependency resolution: `pkg/controller/registry/resolver/`
- Catalog management: `pkg/controller/registry/reconciler/`
- InstallPlan execution: `pkg/controller/install/`

### Finding Utilities
- Owner references: `pkg/lib/ownerutil/`
- Scoped clients: `pkg/lib/scoped/`
- Operator clients: `pkg/lib/operatorclient/`
- Queue informers: `pkg/lib/queueinformer/`

## Anti-Patterns to Avoid

1. **Don't bypass OperatorGroup scoping** - Always use scoped clients for multi-tenancy
2. **Don't modify generated code** - Edit source (CRDs, annotations) and regenerate
3. **Don't skip approval for InstallPlans** - Respect manual approval settings
4. **Don't create CSVs directly** - Use Subscriptions/Catalogs for proper lifecycle
5. **Don't ignore owner references** - Critical for garbage collection

## Resources and Links

- [OLM Documentation](https://olm.operatorframework.io/)
- [Architecture Doc](doc/design/architecture.md)
- [Philosophy](doc/design/philosophy.md)
- [Design Proposals](doc/contributors/design-proposals/)
- [Operator Framework Community](https://github.com/operator-framework/community)
- [OperatorHub.io](https://operatorhub.io/) - Public operator catalog

## Quick Reference

### Resource Short Names
```bash
kubectl get csv          # ClusterServiceVersions
kubectl get sub          # Subscriptions
kubectl get ip           # InstallPlans
kubectl get catsrc       # CatalogSources
kubectl get og           # OperatorGroups
```

### Common Build Targets
```bash
make build build-utils   # Build all binaries
make test unit e2e       # Run tests
make lint verify         # Code quality
make gen-all             # Generate everything
make run-local           # Full local dev environment
```

### Tool Management
Tools are managed via **bingo** (`.bingo/Variables.mk`) for reproducible builds. All tools are version-pinned.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CLAUDE.md](CLAUDE.md) for detailed guidelines.

**Remember**: OLM v0 is in maintenance mode - only critical security fixes and outage issues are accepted!
