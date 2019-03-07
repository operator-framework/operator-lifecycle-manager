# Robust `InstallPlan`s

## Abstract

`InstallPlan` `CustomResource`s can be redesigned to be a generic means of applying Kubernetes manifests to a cluster with robust controls around the lifecycle of each resource generated.

## Goals

- Generalize `InstallPlans` and decouple them from OLM
- Encapsulate a set of resource manifests for application to a cluster in a CustomResource (CR)
- Provide an optional mechanism to make the specs of the set of applied resources runtime immutable - that is, revert any changes to a spec made after the resource is applied
- Provide an optional mechanism to recreate any applied resources that are inadvertently deleted
- Provide an aggregate status of resources in the set
- Restrict resource application based on user RBAC
- Ordered manifests application
- Mechanism to allow cleanup of all applied resources on `InstallPlan` deletion
- Ability for one `InstallPlan` to "replace" another through explicit reference or label selector

## Non-Goals

- Reinvent Helm/Tiller

## InstallPlan Design

The design of an `InstallPlan` can be split into several sub-problems:

- [Manifest Storage](#manifest-storage) - How are the resource manifests to be applied stored?
- [Manifest Validation](#manifest-validation) - How are resource manifests validated?
- [Manifest Application](#manifest-application) - How are resource manifests applied?
- [Lifecycle Controls](#lifecycle-controls) - What options around the lifecycles of applied resources should be available and how are they surfaced?
- [Tranformations](#transformations) - How can the set of resources applied by `InstallPlan` _A_ be replaced by the set applied by `InstallPlan` _B_?

### Manifest Storage

In order to apply a set of Kubenetes resource manifests to a cluster, they must be accessible to the `InstallPlan`'s operator. Ideally, the raw content of these manifests will be stored in Kubernetes resources in order to reduce external dependency requirements and provide a consistent user experience (UX).

There are two main ways acheive this:

- Denormalized: Store the set of raw manifest content in the `InstallPlan`
- Normalized: Define a new `ResourceManifest` type to store raw manifest content for individual resources. Reference the set of `ResourceManifests` from the `InstallPlan` they belong to.

#### Denormalized Approaches

1. The raw manifest content for each resource is stored as an individual entry in an `InstallPlan` CR

   __Pros__:
  
   - All manifests to be applied are packaged into a single unit
   - Atomic operations across stored manifests

   __Cons__:

   - Kubernetes resources bump into etcd size limits. The default limit for Kubernetes is [1MB](https://github.com/kubernetes/kubernetes/issues/19781#issuecomment-172553264)
2. An `InstallPlan` can be served by an extension api-server

   __Pros__:

   - All manifests to be applied are packaged into a single unit
   - Atomic operations across stored manifests
   - Can implement custom storage logic (shard over subresources)
   - More flexible than CRDs

   __Cons__:

   - Need to maintain persistence layer (ha, failover, etc...)
   - Need to implement endpoint logic manually

#### Normalized Approaches

1. Define a new `ResourceManifest` type to store raw manifest content for individual resources. Reference the set of `ResourceManifest`s from the `InstallPlan` they belong to

   __Pros__:

   - Potentially allows for a _reasonably_ arbitrary number of manifests (much smaller `InstallPlan`s)
   - Can be implemented entirely with CRDs

   __Cons__:

   - Manifests are no longer packaged as a single unit
   - No atomic operations across stored manifests
   - `InstallPlan`s can break if `ResourceManifest`s are missing
2. Make `InstallPlan` a _synthetic_ resource served by an ephemeral extension api-server and backed by an `ReferencingInstallPlan` `CustomResource` type which references `ResourceManifest`s¹

   __Pros__:

   - All manifests to be applied are packaged into a single unit (superficially denormalized)
   - All manifests can be manipulated by changes to an `InstallPlan`
   - Potentially allows for a _reasonably_ arbitrary number of manifests (much smaller `InstallPlan`s)
   - Uses Kubernetes' etcd for storage

   __Cons__:

   - Need to implement endpoint logic manually
   - `InstallPlan`s can break if `ResourceManifest`s are missing

> 1 Maybe [initializers](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#initializers) can be used to perform the creation/update of `ReferencingInstallPlan`s and `ResourceManifest`s

### Manifest Application

Resource manifest application should walk through 4 core steps:

1. Determine if being [replaced by another `InstallPlan`](#transformations)

   - If so, bail out before resource application
2. Validate that each manifest is a kubectl-able resource (caveat for CRs and APIService resources?)

   - If not, bail out ...
3. For each resource manifest, determine application path operations:

   - Check the resource's [transformation rules](#transformations) to determine which resources to delete
   - Check the `InstallPlan`'s [lifecycle controls](#lifecycle-controls) for the resource against the current cluster state to determine whether to create, replace, or patch the resource

4. Run operations against cluster (applies and or deletes resources)

#### Application Ordering

Resources can sometimes require other resources to exist before creation (think pods and namespaces). `InstallPlan`s should have a way to provide manifest application ordering.

A basic strategy for application ordering:

- Manifests are organized into lists called _stages_
- An `InstallPlan` contains a list of stages
- When applying `InstllPlan` manifests, each stage is evaluated in ascending index order
- Within each stage, manifests are applied in ascending index order
- An `InstallPlan` specifies optional k8s jobs to run after each stage is evaluated
- If a job fails, the `InstallPlan` should transition to a failed state before attempting to apply manifests again (optional behavior around this?)

#### RBAC

To determine which resources the `InstallPlan` operator is allowed to operate on, each `InstallPlan` must be associated with a `ServiceAccount`. This association could take the form of a field on the `InstallPlan`.

When applying resources for an `InstallPlan`, the `InstallPlan` operator might utilize the respective `ServiceAccount` in one of the following ways:

- [Impersonate](https://kubernetes.io/docs/reference/access-authn-authz/authentication/#user-impersonation) the given `ServiceAccount` to perform all operations
- Impersonate the given `ServiceAccount` to issue a [SelfSubjectAccess](https://kubernetes.io/docs/reference/access-authn-authz/authorization/#checking-api-access) review to check if the `ServiceAccount` has permission to perform all operations
- Build set of cluster rules and check if a `ServiceAccount` can use them via the [RBACAuthorizer](https://github.com/kubernetes/kubernetes/blob/44e369b000fa9c194b5fda142a9d3f62172f27f6/plugin/pkg/auth/authorizer/rbac/rbac.go#L74) (see [RuleChecker](https://github.com/operator-framework/operator-lifecycle-manager/blob/a196e4e694f4f559997e9677109352d93e0fc00f/pkg/controller/install/rule_checker.go#L18) for an example of how this might be done).

### Lifecycle Controls

An `InstallPlan` should have knobs that determine how the `InstallPlan` operator interacts with applied resources. These knobs should specify the following behaviors:

- Revert changes to the specs of applied resources
- Recreate applied resources that are deleted
- Clean up applied resources on `InstallPlan` deletion

Each knob should get a corresponding setting in the `InstallPlanSpec` and depending on the implementation could refer to either:

- A behavior shared by all resources applied by the `InstallPlan`
  - Simple configuration
  - Less flexible
- A behavior specific to an individual resource applied by the `InstallPlan`
  - More complex configuration
  - More flexible (fine grained)
- Both
  - More complex internal rules
  - Easier to have exceptions ("everything but this")
  - Most flexible (course and fine grained)

Lifecycle controls help drive the notion of `InstallPlan`s as a component of immutable infrastructure but provide the flexibility to allow exceptions when neccessary (don't stomp my changes when testing).

### Transformations

A _transformation_ is the ability of an `InstallPlan` to "replace" a set of resources on the cluster with the set from its applied manifests. It allows an `InstallPlan` to provide declaritive upgrade semantics around possibly existing cluster resources, and for `InstallPlan`s to replace eachother.

#### Transformation Rules

A _transformation rule_ is a mapping from a set of resource that an `InstallPlan` provides to a set of resources on the cluster to be replaced. A rule's selected supplanting resources must be members of the `InstallPlan`s manifests, while it's replacing resources may or may not exist on the cluster at the time of resource application.

Following from the definition, a transformation rule has two different selection sets:

- The "from" selection
  - Set of resources to be replaced (incumbent set)
  - May or may not exist on the cluster
  - Must specify `GroupVersionKind`s of selection
  - Could use selectors (label, field)
  - Could use explicit name/namespace
- The "to" selection
  - Set of resources to replace "from" with (supplanting set)
  - Must exist in the `InstallPlan`'s

A configuration to replace an entire `InstallPlan` should be surfaced as well. This allows the `InstallPlan` operator to no-op the lifecycle controls of `InstallPlan`s that are being replaced. Here are a few options for implmenting the `InstallPlan` replacement configuration:

- Top-level "replaces" field
- Special tranformation rule that allows a "from" that selects the surrounding `InstallPlan`
