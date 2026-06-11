# OLM Channel Strategy and Upgrade Semantics

This document describes how OLM channels, upgrade graphs, and OCP version gating work,
and outlines a two-channel release strategy for shipping an operator across multiple OpenShift versions.

## OLM Channels Overview

A **channel** is a named upgrade stream within an operator package. Each channel has a **channel head** —
the latest CSV (ClusterServiceVersion) that no other CSV replaces. When a user creates a `Subscription`
pointing to a channel, OLM installs the channel head (or a `startingCSV` if specified), then follows the
upgrade graph within that channel for future upgrades.

### Upgrade Graph Mechanisms

The upgrade graph within a channel is defined by three mechanisms on each CSV:

- **`replaces`**: points to the single CSV this one directly replaces. OLM walks the `replaces`
  chain and **upgrades one version at a time** until reaching the channel head. For example,
  if v0.1.3 replaces v0.1.2 which replaces v0.1.1, OLM installs v0.1.2 first, then v0.1.3.
- **`skips`**: list of specific CSV names that can upgrade directly to this one. Used to skip
  known-bad releases (e.g., a version with a critical vulnerability).
- **`skipRange`** (annotation `olm.skipRange`): a semver range. If the **channel head** has a
  `skipRange` that includes the currently installed version, OLM **jumps directly to the
  channel head**, bypassing all intermediate versions. This is a direct upgrade, not step-by-step.

**Important**: `skipRange` only applies to the channel head. Intermediate versions with `skipRange`
do not enable skipping — OLM always evaluates whether to jump directly to head first.

When OLM evaluates whether an upgrade is available, it checks (in order of precedence):

1. Channel head in the subscribed catalog source (if `skipRange` on head covers the current version) — **direct jump**.
2. Next CSV that `replaces` the current one in the subscribed source — **step-by-step**.
3. Channel head in another visible catalog source (if `skipRange` covers the current version) — **direct jump**.
4. Next CSV that `replaces` the current one in any visible source — **step-by-step**.

### OCP Version Compatibility

There are **two different mechanisms** for declaring OpenShift version compatibility, serving
different purposes:

#### 1. `com.redhat.openshift.versions` (Build-Time Catalog Filtering)

Defined in `metadata/annotations.yaml`:

```yaml
annotations:
  com.redhat.openshift.versions: "v4.14-v4.16"
```

This annotation is used by **Red Hat's build infrastructure** when generating version-specific
catalog indexes. It controls which bundles are included in each versioned index image
(e.g., `registry.redhat.io/redhat/redhat-operator-index:v4.14` vs `v4.16`).

- **Used by**: Red Hat pipelines, IIB (Index Image Builder), catalog build tooling
- **When**: At catalog build time
- **Effect**: Bundle is excluded from catalog indexes outside the specified range
- **OLM involvement**: None — OLM never sees bundles filtered out at build time

#### 2. `olm.maxOpenShiftVersion` / `olm.minOpenShiftVersion` (Runtime OCP Upgrade Gating)

Defined in `metadata/properties.yaml`:

```yaml
properties:
  - type: olm.maxOpenShiftVersion
    value: "4.17"
  - type: olm.minOpenShiftVersion
    value: "4.14"
```

These properties are used by **OLM at runtime** for two purposes:

1. **Catalog filtering**: OLM filters bundles based on the cluster's OCP version. A bundle with
   `olm.minOpenShiftVersion: "4.16"` won't be visible on a 4.14 cluster.

2. **OCP upgrade gating**: OLM checks installed operators against the **next** OCP minor version.
   If an installed operator's `olm.maxOpenShiftVersion` is less than the next minor, OLM blocks
   the cluster upgrade by setting `Upgradeable=False` on its ClusterOperator.

- **Used by**: OLM resolver, OLM ClusterOperator controller
- **When**: At runtime (install, upgrade, OCP upgrade checks)
- **Effect**: Blocks operator visibility and/or OCP cluster upgrades
- **Flow**: bundle → catalog (indexed) → CSV annotation (`operatorframework.io/properties`)

#### Comparison

| Aspect | `com.redhat.openshift.versions` | `olm.maxOpenShiftVersion` |
|---|---|---|
| Location | `metadata/annotations.yaml` | `metadata/properties.yaml` |
| Used by | Red Hat build pipelines | OLM at runtime |
| When | Catalog index build time | Operator install/upgrade, OCP upgrade |
| Effect | Bundle excluded from index | Bundle hidden + OCP upgrade blocked |
| Format | Range string (`v4.14-v4.16`) | Single version (`4.17`) |

**Recommendation**: Use both. `com.redhat.openshift.versions` ensures your bundle only appears
in appropriate catalog indexes. `olm.maxOpenShiftVersion` provides runtime safety by blocking
OCP upgrades when an incompatible operator is installed.

## Two-Channel Strategy

### Channel 1: `fast`

Ships every new version to all supported OCP versions. Users on this channel always receive
the newest operator release.

```
fast channel:
  v0.1.0 → v0.2.0 → v0.3.0 → v1.0.0 → v1.1.0 → v1.2.0 → v2.0.0
                                                              ↑ head
```

Use `skipRange` liberally (e.g., `olm.skipRange: ">=0.1.0 <2.0.0"`) so users can jump from
any older version directly to the latest without stepping through every intermediate release.

### Channel 2: `stable`

A single channel for all OCP EUS versions. OCP version properties on each bundle control which
versions are visible on which cluster. Only patch/z-stream releases are added within each
OCP version range.

```
stable channel:
  v1.0.0 (OCP 4.14) → v1.0.1 (OCP 4.14) → v1.0.2 (OCP 4.14)

  v1.1.0 (OCP 4.14-4.16, skipRange: ">=1.0.0 <1.1.0") → v1.1.1 (OCP 4.16)

  v2.0.0 (OCP 4.16-4.18, skipRange: ">=1.1.0 <2.0.0") → v2.0.1 (OCP 4.18)
```

**Note**: OCP EUS (Extended Update Support) versions are even-numbered minor releases: 4.14, 4.16,
4.18, etc. EUS versions receive longer support (up to 24+ months). Odd-numbered versions (4.15,
4.17) are non-EUS with shorter support windows.

**EUS-to-EUS upgrades**: The control plane must still upgrade sequentially (4.14 → 4.15 → 4.16) —
you cannot skip minor versions. However, EUS-to-EUS allows you to **pause worker node machine
config pools** during the upgrade, so worker nodes only reboot once (from 4.14 directly to 4.16),
minimizing disruption. The operator must support all versions in the upgrade path (hence the
bridge version with `maxOCP: 4.17` covering 4.14, 4.15, and 4.16).

Each bundle declares its OCP compatibility:

```yaml
# v1.0.0 — OCP 4.14 only (blocks upgrade to 4.15 until operator is upgraded)
olm.properties:
  - type: olm.maxOpenShiftVersion
    value: "4.14"
  - type: olm.minOpenShiftVersion
    value: "4.14"
```

```yaml
# v1.1.0 — bridge version for EUS-to-EUS upgrade (supports 4.14, 4.15, AND 4.16)
# Must support 4.15 because control plane upgrades sequentially: 4.14 → 4.15 → 4.16
olm.properties:
  - type: olm.maxOpenShiftVersion
    value: "4.17"
  - type: olm.minOpenShiftVersion
    value: "4.14"
olm.skipRange: ">=1.0.0 <1.1.0"
```

```yaml
# v1.1.1 — OCP 4.16+ patch (for clusters that completed the EUS upgrade)
olm.properties:
  - type: olm.maxOpenShiftVersion
    value: "4.17"
  - type: olm.minOpenShiftVersion
    value: "4.16"
```

### Upgrade Scenarios

| Scenario | Behavior |
|---|---|
| OCP 4.14 cluster, fresh install | OLM filters the `stable` channel, only sees v1.0.x bundles, installs the latest patch (channel head for that OCP range) |
| OCP 4.14 cluster, patch released | New v1.0.x appears, OLM upgrades automatically |
| OCP 4.14 cluster, bridge version released | v1.1.0 becomes visible (has `minOpenShiftVersion: "4.14"`). OLM upgrades v1.0.0 → v1.1.0 via `skipRange`. This unblocks OCP upgrade to 4.15/4.16 |
| User upgrades OCP 4.14 → 4.16 | See [OCP Upgrade Flow](#ocp-upgrade-flow-and-the-bridge-version-requirement) below |
| OCP 4.16 cluster, fresh install | Only sees v1.1.x, installs the latest patch |
| User on `stable` wants to switch to `fast` | User edits their Subscription to change channel. OLM resolves the new channel head and upgrades if a valid `replaces`/`skipRange` path exists |

## OCP Upgrade Flow and the Bridge Version Requirement

### How OLM Gates OCP Upgrades

OLM continuously checks all installed CSVs against the **next** OCP minor version. If any
operator's `olm.maxOpenShiftVersion` is less than the next minor version, OLM sets:

```
ClusterOperator "operator-lifecycle-manager"
  Condition: Upgradeable=False
  Reason: IncompatibleOperatorsInstalled
```

The Cluster Version Operator (CVO) reads this condition and **blocks the OCP cluster upgrade**
until all operators are compatible.

The logic (implemented in `pkg/controller/operators/openshift/clusteroperator_controller.go`):

1. OLM reads the current OCP version (e.g., 4.14).
2. Computes the next minor version (4.15).
3. For each installed CSV, checks if `olm.maxOpenShiftVersion >= 4.15`.
4. If any CSV fails the check (i.e., `maxOpenShiftVersion < nextMinor`), sets `Upgradeable=False`.

**Example**: On OCP 4.14, an operator with `maxOpenShiftVersion: "4.14"` blocks upgrade to 4.15.
An operator with `maxOpenShiftVersion: "4.15"` allows upgrade to 4.15 (but would block 4.16 later).

### The Deadlock Problem

If the operator version for the next OCP version requires that OCP version to install,
a deadlock occurs:

- v1.0.0 has `maxOpenShiftVersion: "4.15"` → allows 4.15, but blocks upgrade to 4.16.
- v1.1.0 has `minOpenShiftVersion: "4.16"` → not visible on 4.14 or 4.15.
- Result: once on 4.15, can't upgrade OCP to 4.16 without upgrading the operator, but
  can't upgrade the operator without being on 4.16 first.

### Solution: Bridge Versions

Every OCP version transition requires a **bridge version** of the operator that is compatible
with both the current and next OCP version. For EUS-to-EUS upgrades (e.g., 4.14 → 4.16), the
bridge must support the entire range:

```
v1.0.0  → minOCP: 4.14, maxOCP: 4.14   (4.14 only — blocks upgrade to 4.15)
v1.1.0  → minOCP: 4.14, maxOCP: 4.17   (4.14 through 4.16 — bridge version)
v1.1.1  → minOCP: 4.16, maxOCP: 4.17   (4.16 only, patch)
```

The upgrade flow with a bridge version:

1. User is on OCP 4.14 with operator v1.0.0 installed (`maxOCP: 4.14` — blocks upgrade to 4.15).
2. v1.1.0 (bridge) becomes visible on 4.14 because `minOpenShiftVersion: "4.14"`.
3. OLM upgrades the operator: v1.0.0 → v1.1.0 (via `skipRange`).
4. v1.1.0 has `maxOpenShiftVersion: "4.17"`:
   - On 4.14: `4.17 >= 4.15` → upgrade to 4.15 allowed
   - On 4.15: `4.17 >= 4.16` → upgrade to 4.16 allowed
5. User upgrades OCP to 4.16 (control plane goes 4.14 → 4.15 → 4.16 sequentially).
6. On 4.16, subsequent patches (v1.1.1, v1.1.2) continue as normal.

**Automatic vs. Manual Approval**:

With `installPlanApproval: Automatic` (default), step 3 happens automatically as soon as the
bridge version appears in the catalog. The user doesn't need to take any action — OLM upgrades
the operator, which unblocks the OCP upgrade. This is the seamless experience.

With `installPlanApproval: Manual`, the user must approve the operator upgrade (v1.0.0 → v1.1.0)
before the OCP upgrade becomes unblocked. If they attempt to upgrade OCP first, the CVO will
block it until they approve the pending operator InstallPlan.

### Timeline Diagram

```
OCP 4.14                              OCP 4.16
─────────────────────────────────────────────────────────────
operator v1.0.0 ──► v1.1.0 (bridge) ──► [OCP upgrade] ──► v1.1.1 (patch)
                     supports 4.14-4.16                    4.16 only
```

## Single `stable` Channel vs. Per-EUS Channels

| Aspect | Single `stable` channel | Per-EUS channels (`stable-4.14`, `stable-4.16`) |
|---|---|---|
| User experience | Simpler — one subscription, never needs editing | User must manually switch channel when upgrading OCP |
| OCP upgrade | Automatic — operator upgrades when bridge version becomes visible | Manual — user must change channel in Subscription |
| Catalog complexity | All bundles in one channel, filtered by OCP version properties | Separate channels, each self-contained |
| Risk | Relies on correct `minOpenShiftVersion`/`maxOpenShiftVersion` — a misconfigured property could expose incompatible versions to clusters | Channel provides hard isolation — even with wrong properties, users only see versions in their subscribed channel |

## Key Implementation References

- Channel and upgrade graph resolution: `pkg/controller/registry/resolver/resolver.go`
- OCP upgrade gating logic: `pkg/controller/operators/openshift/clusteroperator_controller.go`
- `maxOpenShiftVersion` parsing: `pkg/controller/operators/openshift/helpers.go`
- Upgrade predicate matching (`replaces`, `skips`, `skipRange`): `pkg/controller/registry/resolver/cache/predicates.go`
- Operator upgrade conditions: `pkg/controller/operators/olm/operatorconditions.go`
- Properties annotation processing: `pkg/controller/registry/resolver/projection/properties.go`
- Upgrade strategy documentation: `doc/design/how-to-update-operators.md`
