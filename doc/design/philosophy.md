# Goals

The goal of the Operator Lifecycle Manager and Cloud Service Catalog is to manage common aspects of open cloud services, including:

**Lifecycle** 

 * Managing the upgrades and lifecycle for operators (much as operators manage the upgrades and lifecycle for the resources they operate)

**Discovery** 

 * What operators exist on the cluster? What are the things they operate? What operators are available for installing into a cluster?

**Packaging**

 * A standard way to distribute, install and upgrade an operator and its dependencies

**Interaction**

 * By standardizing the other three, provide a standard way to interact with cloud services and user-defined open cloud services via both the CLI and the OpenShift web console. 

# Design

We achieve the desired goals by standardizing packaging and being opinionated about the way a user interacts with an operator.

These are our requirements:

**Namespacing**

 * An operator and the resources it operates *must* be restricted to one namespace. This is the only reasonable way to manage a multi-tenant cluster and enforce RBAC and chargeback on operator resources.

**Custom Resources**	

 * The primary way a user should interact with an operator must be via writing and reading Custom Resources

 * An operator should declare the CRDs it owns and manages, as well as those that it expects to exist (but be managed by other operators).

 * Configuration of operator behavior should be represented as fields on a CRD

**Dependency Resolution**

 * Operators will only need to worry about packaging themselves and the resources they manage, not linking in the world in order to run. 

 * Dynamic libraries, not fat binaries. As an example, the vault operator container should not also include the etcd operator container, but should rather take a dependency on Etcd that OLM will resolve. This is analogous to dynamic vs. static linking.

 * To achieve this, operators will need to define their dependencies.

**Repeatable/Recoverable Deployment**

 * Resolving dependencies and installing a set of resources into the cluster should be repeatable. (think glide.lock)

 * It shouldn't matter if any critical software fails during the install process (recoverable).

**Garbage Collection**

 * We should rely on kubernetes garbage collection where possible.

 * Deleting a top level ClusterService should remove all running resources related to it

 * Deleting a top level ClusterService should **not** remove any resources managed by another ClusterService (i.e. even if Etcd ClusterService is installed because it's a Vault dependency, we don't remove the Etcd ClusterService when Vault is deleted, only the EtcdClusters managed by any VaultService)

**Labelling / Resource Discovery**	

 * ClusterService resources should provide:

    * Labels, which will be propagated to sub-resources

    * Label selectors, which can be used to find related sub-resources

  * This labelling pattern is taken directly from the label and selector fields of Deployment

# Implementation

OLM defines packaging formats for operators. These are:

## ClusterServiceVersion

 * Represents a particular version of the ClusterService and the operator managing it

 * References global named identity (e.g. "etcd") for the ClusterService

     * `apt-get install ruby` actually installs `mruby-2.3`

 * Has metadata about the package (maintainers, icon, etc)

 * Declares owned CRDs

     * These are the CRDs directly owned by the Operator. `EtcdCluster` is owned by the Etcd `ClusterServiceVersion` but not the Vault `ClusterServiceVersion`

 * Declares required CRDs

     * These are CRDs required by the Operator but not directly managed by it.  `EtcdCluster` is required by the Vault `ClusterServiceVersion` but not managed by it.

 * Declares cluster requirements

     * An operator may require a pull secret, a config map, or the availability of a cluster feature.

 * Provides an Install Strategy 

     * The install strategy tells OLM how to actually create resources in the cluster.

     * Currently the only strategy is `deployment`, which specifies a Kubernetes Deployment
      
     * Future install strategies include `image`, `helm`, and upstream community strategies

 * Roughly equivalent to dpkg - you can install a dpkg manually, but if you do, dependency resolution is up to you. 

## InstallPlan

 * An install plan is a declaration by a user that they want a particular ClusterService in a namespace. (i.e. `apt-get install midori`)

 * The install plan gets "resolved" to a concrete set of resources

     * Much like apt reads the dependency information from dpkgs to come up with a set of things to install, OLM reads the dependency graph from ClusterServiceVersions to come up with a set of resources to install

 * The resolved set of resources is written back to the InstallPlan

     * Users can set these to auto-approve (apt-get install -y) or require manual review

     * The record of these resources is kept in cluster so that installs are repeatable/recoverable/inspectable, but can be cleaned up once completed if desired.

## CatalogSource

 * A catalog source binds a name to a url where ClusterServices can be downloaded

 * The ClusterService cache is updated from this URL

## Subscription

 * A subscription configures when and how to update a ClusterService

 * Binds a ClusterService to a channel in a CatalogSource

 * Configures the update strategy for a ClusterService (automatic, manual approval, etc)

# Components

We have two major components that handle the resources described above

 **OLM Operator**

 * Watches for ClusterServiceVersions in a namespace and checks that requirements are met. If so, runs the service install strategy for the ClusterServiceVersion and installs the resource into the cluster. For example for a `deployment` strategy installation is achieved by creating a Kubernetes Deployment, which gets resolved by the Deployment controller. 

 **Service Catalog Operator**

 * Has a cache of CRDs and ClusterServiceVersions, indexed by name

 * Watches for InstallPlans created by a user (unresolved)

     1. Finds the ClusterServiceVersion matching the cluster service name requested, adds it as a resolved resource.

     2. For each managed or required CRD, adds it as a resolved resource.

     3. For each required CRD, finds the ClusterServiceVersion that manages it

     4. Goto 1

 * Watches for resolved InstallPlans and creates all of the discovered resources for it (if approved by a user or automatically)

 * Watches for CatalogSources / Subscriptions and creates InstallPlans based on them

# FAQ

**What if I want lifecycle/packaging/discovery for kubernetes, but don't want to write an operator?**

If you don't want to write an operator, the thing you want to package probably fits one of the standard shapes of software that can be deployed on a cluster. You can take advantage of OLM by writing a package that binds your application to one of our standard operators, like [helm-app-operator-kit](https://github.com/coreos/helm-app-operator-kit).

If your use-case doesn't fit one of our standard operators, that means you have domain-specific operational knowledge you need to encode into an operator, and you can take advantage of our [Operator SDK](https://github.com/operator-framework/operator-sdk) for common operator tasks.

**Why are dependencies between operators expressed as a dependency on a CRD?**

This decouples the actual dependency from the operation of the dependency. For example, Vault requires an EtcdCluster, but we should be able to update the etcd operator out of step with the vault operator.

**Who installs the CRDs that get managed by operators?**

The CRD definitions are kept in the service catalog cache. During InstallPlan resolution, they are pulled from the cache and added as resources to be created in the installplan's status block. An operator writer only needs to write the name (name/group/version) of the CRD they depend on and it will exist in the cluster before the operator starts.

(This ignores the publishing aspect of this, which is TBD)

**How are updates handled?**

An operator can be updated by updating the service catalog cache and running a new install plan. ClusterServiceVersions specify the version they replace, so that OLM knows to run both old and new simultaneously while resource ownership is transitioned. This is done with OwnerReferences in kubernetes. OLM garbage collects old versions of the operator.

This requires operators being aware of owner references, and in particular the `controller` flag and gc policy options. 

Updates are discovered by either updating the service cache and running a new InstallPlan, or by configuring "subscriptions" for particular ClusterServices.

**What if there are multiple operators that "own" or "manage" a CRD?**

Initially, we require that there be only one owner package for a CRD in the service catalog cache. If there is a use case for multiple owners, the option will be surfaced on the InstallPlan, and a user will manually resolve the choice.

