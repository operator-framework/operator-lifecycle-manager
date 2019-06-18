[![Docker Repository on Quay](https://quay.io/repository/coreos/alm/status?token=ccfd2fde-446d-4d82-88a8-4386f8deaab0 "Docker Repository on Quay")](https://quay.io/repository/coreos/alm) [![Docker Repository on Quay](https://quay.io/repository/coreos/catalog/status?token=b5fc43ed-9f5f-408b-961b-c8493e983da5 "Docker Repository on Quay")](https://quay.io/repository/coreos/catalog)[![pipeline status](https://gitlab.com/operator-framework/operator-framework_operator-lifecycle-manager/badges/master/pipeline.svg)](https://gitlab.com/operator-framework/operator-framework_operator-lifecycle-manager/pipelines)

<img src="/logo.svg" height="125px" alt="Operator Lifecycle Manager"></img>

<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
<!-- **Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*-->

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Getting Started](#getting-started)
  - [Installation](#installation)
  - [User Interface (Running the console Locally)](#user-interface-running-the-console-locally)
  - [Subscribe to a Package and Channel](#subscribe-to-a-package-and-channel)
  - [End-to-end example](#end-to-end-example)
  - [Contributing with Operator catalogue](#contributing-with-operator-catalogue)
- [Key Concepts](#key-concepts)
  - [Kubernetes-native Applications](#kubernetes-native-applications)
  - [CustomResourceDefinitions](#customresourcedefinitions)
  - [Descriptors](#descriptors)
  - [Dependency Resolution](#dependency-resolution)
    - [Granularity](#granularity)
  - [Discovery, Catalogs, and Automated Upgrades](#discovery-catalogs-and-automated-upgrades)
- [Samples](#samples)
- [Contributing](#contributing)
- [Reporting bugs](#reporting-bugs)
- [License](#license)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Overview

This project is a component of the [Operator Framework][of-home], an open source toolkit to manage Kubernetes native applications, called Operators, in an effective, automated, and scalable way. Read more in the [introduction blog post][of-blog].

[Operators][operator_link] make it easy to manage complex stateful applications on top of Kubernetes. However writing an operator today can be difficult because of challenges such as using low level APIs, writing boilerplate, and a lack of modularity which leads to duplication.

OLM extends Kubernetes to provide a declarative way to install, manage, and upgrade operators and their dependencies in a cluster.

It also enforces some constraints on the components it manages in order to ensure a good user experience.

This project enables users to do the following:

* Define applications as a single Kubernetes resource that encapsulates requirements and metadata
* Install applications automatically with dependency resolution or manually with nothing but `kubectl`
* Upgrade applications automatically with different approval policies

This project does not:

* Replace [Helm](https://github.com/kubernetes/helm)
* Turn Kubernetes into a [PaaS](https://en.wikipedia.org/wiki/Platform_as_a_service)

# Prerequisites

- [dep][dep_tool] version v0.5.0+.
- [git][git_tool]
- [go][go_tool] version v1.12+.
- [docker][docker_tool] version 17.03+.
  - Alternatively [podman][podman_tool] `v1.2.0+` or [buildah][buildah_tool] `v1.7+`
- [kubectl][kubectl_tool] version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.
- Optional: [`delve`](https://github.com/go-delve/delve/tree/master/Documentation/installation) version 1.2.0+ (for `up local --enable-delve`).

# Getting Started 

## Installation

Install OLM on a Kubernetes or OpenShift cluster by following the [installation guide].

**NOTE:** OLM is installed by default in OpenShift 4.0 and above.

## User Interface (Running the console Locally)

Use the OpenShift admin console (compatible with upstream Kubernetes) to interact with and visualize the resources managed by OLM. Create subscriptions, approve install plans, identify Operator-managed resources, and more.

Ensure `kubectl` is pointing at a cluster and run:

```shell
$ make run-console-local
```

Then visit `http://localhost:9000` to view the console.

## Subscribe to a Package and Channel

Cloud Services can be installed from the catalog by subscribing to a channel in the corresponding package.

**Subscription detail view:**
![screenshot_20180628_165240](https://user-images.githubusercontent.com/11700385/42060125-c3cde42c-7af3-11e8-87ec-e5910a554902.png)

NOTE: The operators available in the catalogue by default are that ones which are published in https://operatorhub.io/.

## End-to-end example

For a complete end-to-end example of how OLM fits into the Operator Framework, see the [Operator Framework Getting Started Guide](https://github.com/operator-framework/getting-started). Also, see [Getting Started on OperatorHub.io](https://operatorhub.io/getting-started).

## Contributing with Operator catalogue

Check how to [Publish your Operator on OperatorHub.io](https://operatorhub.io/contribute).

# Key Concepts

## Kubernetes-native Applications

An Operator is an application-specific controller that extends the Kubernetes API to create, configure, manage, and operate instances of complex applications on behalf of a user.

OLM requires that applications be managed by an operator, but that doesn't mean that each application must write one from scratch. Depending on the level of control required you may:

- Package up an existing set of resources for OLM with [helm-app-operator-kit](https://github.com/operator-framework/helm-app-operator-kit) without writing a single line of go.
- Use the [operator-sdk](https://github.com/operator-framework/operator-sdk) to quickly build an operator from scratch.

The primary vehicle for describing operator requirements with OLM is a [`ClusterServiceVersion`](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/Documentation/design/building-your-csv.md). Once you have an application packaged for OLM, you can deploy it with OLM by creating its `ClusterServiceVersion` in a namespace with a supporting [`OperatorGroup`](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/Documentation/design/operatorgroups.md).

ClusterServiceVersions can be collected into `CatalogSource`s which will allow automated installation and dependency resolution via an `InstallPlan`, and can be kept up-to-date with a `Subscription`.

Learn more about the components used by OLM by reading about the [architecture] and [philosophy].

## CustomResourceDefinitions

OLM standardizes interactions with operators by requiring that the interface to an operator be via the Kubernetes API. Because we expect users to define the interfaces to their applications, OLM currently uses CRDs to define the Kubernetes API interactions.  

Examples: [EtcdCluster CRD](https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/etcdcluster.crd.yaml), [EtcdBackup CRD](https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/etcdbackup.crd.yaml)

## Descriptors

OLM introduces the notion of “descriptors” of both `spec` and `status` fields in kubernetes API responses. Descriptors are intended to indicate various properties of a field in order to make decisions about their content. For example, this can drive connecting two operators together (e.g. connecting the connection string from a mysql instance to a consuming application) and be used to drive rich interactions in a UI.

[See an example of a ClusterServiceVersion with descriptors][csv_sample]

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

Example: [etcd package][etcd]

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

Catalogs are served internally over a grpc interface to OLM from [operator-registry][registry] pods.

# Samples

To explore any operator samples built using the operator-sdk, see the [operator-sdk-samples][samples].

# Contributing

See [CONTRIBUTING][contrib] for details on submitting patches and the contribution workflow.

# Reporting bugs

See [reporting bugs][bug_guide] for details about reporting any issues.

# License

Operator SDK is under Apache 2.0 license. See the [LICENSE][license_file] file for details.

[operator_link]: https://coreos.com/operators/
[samples]: https://github.com/operator-framework/operator-sdk-samples
[of-home]: https://github.com/operator-framework
[of-blog]: https://coreos.com/blog/introducing-operator-framework
[contrib]: ./CONTRIBUTING.MD
[bug_guide]:./doc/dev/reporting_bugs.md
[license_file]: ./LICENSE
[dep_tool]: https://golang.github.io/dep/docs/installation.html
[git_tool]: https://git-scm.com/downloads
[go_tool]: https://golang.org/dl/
[docker_tool]: https://docs.docker.com/install/
[podman_tool]: https://github.com/containers/libpod/blob/master/install.md
[buildah_tool]: https://github.com/containers/buildah/blob/master/install.md
[kubectl_tool]: https://kubernetes.io/docs/tasks/tools/install-kubectl/
[registry]: https://github.com/operator-framework/operator-registry 
[etcd]: https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/etcd.package.yaml
[csv_sample]: https://github.com/operator-framework/community-operators/blob/master/community-operators/etcd/etcdoperator.v0.9.2.clusterserviceversion.yaml
[architecture]: /doc/design/architecture.md
[philosophy]: /doc/design/philosophy.md
[installation guide]: /doc/install/install.md