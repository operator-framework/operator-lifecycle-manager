[![Docker Repository on Quay](https://quay.io/repository/coreos/alm/status?token=ccfd2fde-446d-4d82-88a8-4386f8deaab0 "Docker Repository on Quay")](https://quay.io/repository/coreos/alm) [![Docker Repository on Quay](https://quay.io/repository/coreos/catalog/status?token=b5fc43ed-9f5f-408b-961b-c8493e983da5 "Docker Repository on Quay")](https://quay.io/repository/coreos/catalog)[![pipeline status](https://gitlab.com/operator-framework/operator-framework_operator-lifecycle-manager/badges/master/pipeline.svg)](https://gitlab.com/operator-framework/operator-framework_operator-lifecycle-manager/pipelines)

<img src="/logo.svg" height="125px" alt="Operator Lifecycle Manager"></img>

This project is a component of the [Operator Framework](https://github.com/operator-framework), an open source toolkit to manage Kubernetes native applications, called Operators, in an effective, automated, and scalable way. Read more in the [introduction blog post](https://coreos.com/blog/introducing-operator-framework).

OLM extends Kubernetes to provide a declarative way to install, manage, and upgrade operators and their dependencies in a cluster.

It also enforces some constraints on the components it manages in order to ensure a good user experience.

This project enables users to do the following:

* Define applications as a single Kubernetes resource that encapsulates requirements and metadata
* Install applications automatically with dependency resolution or manually with nothing but `kubectl`
* Upgrade applications automatically with different approval policies

This project does not:

* Replace [Helm](https://github.com/kubernetes/helm)
* Turn Kubernetes into a [PaaS](https://en.wikipedia.org/wiki/Platform_as_a_service)

## Getting Started 

#### Installation

Install OLM on a Kubernetes or OpenShift cluster by following the [installation guide].

For a complete end-to-end example of how OLM fits into the Operator Framework, see the [Operator Framework Getting Started Guide](https://github.com/operator-framework/getting-started).

#### Kubernetes-native Applications

An Operator is an application-specific controller that extends the Kubernetes API to create, configure, manage, and operate instances of complex applications on behalf of a user.

OLM requires that applications be managed by an operator, but that doesn't mean that each application must write one from scratch. Depending on the level of control required you may:

- Package up an existing set of resources for OLM with [helm-app-operator-kit](https://github.com/operator-framework/helm-app-operator-kit) without writing a single line of go.
- Use the [operator-sdk](https://github.com/operator-framework/operator-sdk) to quickly build an operator from scratch.

Once you have an application packaged for OLM, you can deploy it with OLM by writing a `ClusterServiceVersion`.

ClusterServiceVersions can be collected into `CatalogSource`s which will allow automated installation and dependency resolution via an `InstallPlan`, and can be kept up-to-date with a `Subscription`.

Learn more about the components used by OLM by reading about the [architecture] and [philosophy].

[architecture]: /Documentation/design/architecture.md
[philosophy]: /Documentation/design/philosophy.md
[installation guide]: /Documentation/install/install.md


# Key Concepts

## CustomResourceDefinitions

OLM standardizes interactions with operators by requiring that the interface to an operator be via the Kubernetes API. Because we expect users to define the interfaces to their applications, OLM currently uses CRDs to define the Kubernetes API interactions.  

Examples: [EtcdCluster CRD](deploy/chart/catalog_resources/rh-operators/etcdcluster.crd.yaml), [EtcdBackup CRD](deploy/chart/catalog_resources/rh-operators/etcdbackup.crd.yaml)

## Descriptors

OLM introduces the notion of “descriptors” of both `spec` and `status` fields in kubernetes API responses. Descriptors are intended to indicate various properties of a field in order to make decisions about their content. For example, this can drive connecting two operators together (e.g. connecting the connection string from a mysql instance to a consuming application) and be used to drive rich interactions in a UI.

[See an example of a ClusterServiceVersion with descriptors](deploy/chart/catalog_resources/rh-operators/etcdoperator.v0.9.2.clusterserviceversion.yaml)

## Dependency Resolution

To minimize the effort required to run an application on kubernetes, OLM handles dependency discovery and resolution of applications running on OLM.

This is achieved through additional metadata on the application definition. Each operator must define:

 - The CRDs that it is responsible for managing. 
   - e.g., the etcd operator manages `EtcdCluster`.
 - The CRDs that it depends on. 
   - e.g., the vault operator depends on `EtcdCluster`, because Vault is backed by etcd.

Basic dependency resolution is then possible by finding, for each “required” CRD, the corresponding operator that manages it and installing it as well. Dependency resolution can be further constrained by the way a user interacts with catalogs.

### Granularity

Dependency resolution is driven through the `(Group, Version, Kind)` of CRDs. This means that no updates can occur to a given CRD (of a particular Group, Version, Kind) unless they are completely backward compatible.

There is no way to express a dependency on a particular version of an operator (e.g. `etcd-operator v0.9.0`) or application instance (e.g. `etcd v3.2.1`). This encourages application authors to depend on the interface and not the implementation.

## Discovery, Catalogs, and Automated Upgrades
OLM has the concept of catalogs, which are repositories of application definitions and CRDs. 	

Catalogs contain a set of Packages, which map “channels” to a particular application definition. Channels allow package authors write different upgrade paths for different users (e.g. alpha vs. stable). 

Example: [etcd package](deploy/chart/catalog_resources/rh-operators/etcd.package.yaml)

Users can subscribe to channels and have their operators automatically updated when new versions are released.

Here's an example of a subscription:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: etcd
  namespace: local 
spec:
  channel: alpha
  name: etcd
  source: rh-operators
```

This will keep the etcd `ClusterServiceVersion` up to date as new versions become available in the catalog.

### User Interface

Use the OpenShift admin console (compatible with upstream Kubernetes) to interact with and visualize the resources managed by OLM. Create subscriptions, approve install plans, identify Operator-managed resources, and more.

Ensure `kubectl` is pointing at a cluster and run:

```shell
$ ./scripts/run_console_local.sh
```

Then visit `http://localhost:9000` to view the console.

**Subscription detail view:**
![screenshot_20180628_165240](https://user-images.githubusercontent.com/11700385/42060125-c3cde42c-7af3-11e8-87ec-e5910a554902.png)
