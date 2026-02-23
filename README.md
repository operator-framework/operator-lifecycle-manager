<img src="logo/logo.svg#gh-light-mode-only" height="125px" alt="Operator Lifecycle Manager"></img>
<img src="logo/logo-dark-mode.svg#gh-dark-mode-only" height="125px" alt="Operator Lifecycle Manager"></img>

[![Container Repository on Quay.io](https://quay.io/repository/operator-framework/olm/status "Container Repository on Quay.io")](https://quay.io/repository/operator-framework/olm)
[![License](http://img.shields.io/:license-apache-blue.svg)](http://www.apache.org/licenses/LICENSE-2.0.html)
[![Go Report Card](https://goreportcard.com/badge/github.com/operator-framework/operator-lifecycle-manager)](https://goreportcard.com/report/github.com/operator-framework/operator-lifecycle-manager)
[![Slack Channel](https://img.shields.io/badge/chat-4A154B?logo=slack&logoColor=white "Slack Channel")](https://kubernetes.slack.com/archives/C0181L6JYQ2)

---
> [!WARNING]  
> This repository is associated with **Operator Lifecycle Manager** version 0 (v0) which is in maintenance mode. The community is excited to be working on a new design (v1) which intends to overcome a number of limitations of the v0 architecture, the primary repository for which is [operator controller](https://www.github.com/operator-framework/operator-controller).  

In maintenance mode, we have the following changes to support expectations:
1. We will accept no new features.
2. We will accept no non-critical outage issues, defined here as a cluster-wide outage with no workaround.
3. We will close backlog issues which do not meet the above criteria.
4. We will continue to update the project to address emergent vulnerabilities on a best-effort basis.

If you have any questions, please engage with us: [![Slack Channel](https://img.shields.io/badge/chat-4A154B?logo=slack&logoColor=white "Slack Channel")](https://kubernetes.slack.com/archives/C0181L6JYQ2)

---

## Documentation

User documentation can be found on the [OLM website][olm-docs].

## Overview

This project is a component of the [Operator Framework](https://github.com/operator-framework), an open source toolkit to manage Kubernetes native applications, called Operators, in an effective, automated, and scalable way. Read more in the [introduction blog post](https://operatorhub.io/what-is-an-operator) and learn about practical use cases at the [OLM website][olm-docs].

OLM extends Kubernetes to provide a declarative way to install, manage, and upgrade Operators and their dependencies in a cluster. It provides the following features:

### Over-the-Air Updates and Catalogs

Kubernetes clusters are being kept up to date using elaborate update mechanisms today, more often automatically and in the background. Operators, being cluster extensions, should follow that. OLM has a concept of catalogs from which Operators are available to install and being kept up to date. In this model OLM allows maintainers granular authoring of the update path and gives commercial vendors a flexible publishing mechanism using channels.

### Dependency Model

With OLMs packaging format Operators can express dependencies on the platform and on other Operators. They can rely on OLM to respect these requirements as long as the cluster is up. In this way, OLMs dependency model ensures Operators stay working during their long lifecycle across multiple updates of the platform or other Operators.

### Discoverability

OLM advertises installed Operators and their services into the namespaces of tenants. They can discover which managed services are available and which Operator provides them. Administrators can rely on catalog content projected into a cluster, enabling discovery of Operators available to install.

### Cluster Stability

Operators must claim ownership of their APIs. OLM will prevent conflicting Operators owning the same APIs being installed, ensuring cluster stability.

### Declarative UI controls

Operators can behave like managed service providers. Their user interface on the command line are APIs. For graphical consoles OLM annotates those APIs with descriptors that drive the creation of rich interfaces and forms for users to interact with the Operator in a natural, cloud-like way.

## Prerequisites

- [git][git_tool]
- [go][go_tool] version v1.12+.
- [docker][docker_tool] version 17.03+.
  - Alternatively [podman][podman_tool] `v1.2.0+` or [buildah][buildah_tool] `v1.7+`
- [kubectl][kubectl_tool] version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

## Getting Started

Check out the [Getting Started][olm-getting-started] section in the docs.

### Installation

Install OLM on a Kubernetes cluster by following the [installation guide][installation-guide].

For a complete end-to-end example of how OLM fits into the Operator Framework, see the [Operator Framework website](https://operatorframework.io/about/) and the [Getting Started guide on OperatorHub.io](https://operatorhub.io/getting-started).

## Contributing your Operator

Have an awesome Operator you want to share? Checkout the [publishing docs](https://operatorhub.io/contribute) to learn about contributing to [OperatorHub.io](https://operatorhub.io/).

## Subscribe to a Package and Channel

Cloud Services can be installed from the catalog by subscribing to a channel in the corresponding package.

## Kubernetes-native Applications

An Operator is an application-specific controller that extends the Kubernetes API to create, configure, manage, and operate instances of complex applications on behalf of a user.

OLM requires that applications be managed by an operator, but that doesn't mean that each application must write one from scratch. Depending on the level of control required you may:

- Package up an existing set of resources for OLM with [helm-app-operator-kit](https://github.com/operator-framework/helm-app-operator-kit) without writing a single line of go.
- Use the [operator-sdk](https://github.com/operator-framework/operator-sdk) to quickly build an operator from scratch.

The primary vehicle for describing operator requirements with OLM is a [`ClusterServiceVersion`](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/doc/design/building-your-csv.md). Once you have an application packaged for OLM, you can deploy it with OLM by creating its `ClusterServiceVersion` in a namespace with a supporting [`OperatorGroup`](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/doc/design/operatorgroups.md).

ClusterServiceVersions can be collected into `CatalogSource`s which will allow automated installation and dependency resolution via an `InstallPlan`, and can be kept up-to-date with a `Subscription`.

Learn more about the components used by OLM by reading about the [architecture] and [philosophy].

# Key Concepts

## CustomResourceDefinitions

OLM standardizes interactions with operators by requiring that the interface to an operator be via the Kubernetes API. Because we expect users to define the interfaces to their applications, OLM currently uses CRDs to define the Kubernetes API interactions.

Examples: [EtcdCluster CRD](https://github.com/redhat-openshift-ecosystem/community-operators-prod/blob/main/operators/etcd/0.9.4/manifests/etcdclusters.etcd.database.coreos.com.crd.yaml),
[EtcdBackup CRD](https://github.com/redhat-openshift-ecosystem/community-operators-prod/blob/main/operators/etcd/0.9.4/manifests/etcdbackups.etcd.database.coreos.com.crd.yaml)

## Descriptors

OLM introduces the notion of “descriptors” of both `spec` and `status` fields in kubernetes API responses. Descriptors are intended to indicate various properties of a field in order to make decisions about their content. For example, this can drive connecting two operators together (e.g. connecting the connection string from a mysql instance to a consuming application) and be used to drive rich interactions in a UI.

[See an example of a ClusterServiceVersion with descriptors](https://github.com/redhat-openshift-ecosystem/community-operators-prod/blob/main/operators/etcd/0.9.2/etcdoperator.v0.9.2.clusterserviceversion.yaml)

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

Catalogs contain a set of Packages, which map “channels” to a particular application definition. Channels allow package authors to write different upgrade paths for different users (e.g. alpha vs. stable).

Example: [etcd package](https://github.com/redhat-openshift-ecosystem/community-operators-prod/blob/main/operators/etcd/etcd.package.yaml)

Users can subscribe to channels and have their operators automatically updated when new versions are released.

Here's an example of a subscription:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: etcd
  namespace: olm
spec:
  channel: singlenamespace-alpha
  name: etcd
  source: operatorhubio-catalog
  sourceNamespace: olm
```

This will keep the etcd `ClusterServiceVersion` up to date as new versions become available in the catalog.

Catalogs are served internally over a grpc interface to OLM from [operator-registry](https://github.com/operator-framework/operator-registry) pods.  Catalog data such as `bundles` are documented [there](https://github.com/operator-framework/operator-registry#manifest-format) as well.

## Samples

To explore any operator samples using the OLM, see the [https://operatorhub.io/](https://operatorhub.io/) and its resources in [Community Operators](https://github.com/k8s-operatorhub/community-operators/tree/main/operators).

## Community and how to get involved

- [Operator framework community][operator-framework-community]
- [Communication channels][operator-framework-communication]
- [Project meetings][operator-framework-meetings]

## Contributing

Check out the [contributor documentation][contributor-documentation]. Also, see the [proposal docs][proposals_docs] and issues for ongoing or planned work.

## Reporting bugs

See [reporting bugs][bug_guide] for details about reporting any issues.

## Reporting flaky tests

See [reporting flaky tests][flaky_test_guide] for details about reporting flaky tests.

## License

Operator Lifecycle Manager is under Apache 2.0 license. See the [LICENSE][license_file] file for details.

[architecture]: /doc/design/architecture.md
[philosophy]: /doc/design/philosophy.md
[proposals_docs]: ./doc/contributors/design-proposals
[license_file]:./LICENSE
[bug_guide]:./doc/dev/reporting_bugs.md
[flaky_test_guide]:./doc/dev/reporting_flakes.md
[git_tool]:https://git-scm.com/downloads
[go_tool]:https://golang.org/dl/
[docker_tool]:https://docs.docker.com/install/
[podman_tool]:https://github.com/containers/libpod/blob/master/install.md
[buildah_tool]:https://github.com/containers/buildah/blob/master/install.md
[kubectl_tool]:https://kubernetes.io/docs/tasks/tools/install-kubectl/
[olm-docs]: https://olm.operatorframework.io/
[operator-framework-community]: https://github.com/operator-framework/community
[operator-framework-communication]: https://github.com/operator-framework/community#get-involved
[operator-framework-meetings]: https://github.com/operator-framework/community#meetings
[contributor-documentation]: ./CONTRIBUTING.md
[olm-getting-started]: https://olm.operatorframework.io/docs/getting-started/
[installation-guide]: doc/install/install.md
