# Installed Operator API

Status: Pending

Version: Alpha

Implementation Owner: TBD

## Motivation

Today, information about operators configured to watch a given namespace is surfaced via "copied" `ClusterServiceVersion`s (`CSV`s). This means that for each __<CSV, watched namespace>__ pair, OLM must manage a copied CSV instance. Additionally, `CSV`s often contain large amounts of metadata, most notably a [base64 encoded icon](https://github.com/operator-framework/operator-lifecycle-manager/blob/18b6c0d2edba2534b4726138b225fff066aae99a/manifests/0000_50_olm_03-clusterserviceversion.crd.yaml#L114). As of OpenShift 4.2, there are more than a dozen namespaces that globally installed operators have their `CSV`s copied to, which causes OLM's `CSV` cache (and memory consumption) to grow large (~150MB) with a fairly low number of operators installed (~5). 

Due to these factors, an additional means of providing this information is required.

## Proposal

Define a new read-only API resource, `installed.packages.operators.coreos.com`, that surfaces metadata about an operator installed and configured to watch a namespace. Back that API by an extension api-server that _synthesizes_ those resources on request.

### Naming

The resource will share a name with the `CSV` of the installed operator it's tracking.

### Namespaces

The resource will be namespaced. Additionaly, it will exist only when an operator that provides a package matching its `metadata.name` is installed and configured to watch its namespace.

### Spec and Status

The resource __will not__ define a `spec` field because it's intended to aggregate the state of existing resources. In other words, there is no meaning in declaring a _desired state_. However, It __will__ define a `status` field that embeds the latest `CSV` and `Subscription`. Embedding entire resources instead of their spec or status directly allows tracked resources to be added or removed more easily in subsequent API changes.

### Labels and Annotations

Labels of the format `operators.coreos.com/<short>: <name>` will be added for each resource tracked in the `status` (see the [k8s docs](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set) for the inspiration behind the key prefix). For tracking `CSV`s and `Subscription`s, the label keys would include `operators.coreos.com/csv` and `operators.coreos.com/sub`.

Labels on `Installed` resources must provide enough information to be queriable by the [openshift console](https://github.com/openshift/console). As such, labels may need to be projected from tracked resources or added as needed.

### The `Installed` Resource

Example `Installed` resource for the `etcdoperator.v0.9.4-clusterwide` `CSV`:

```yaml
apiVersion: v1alpha2/packages.operators.coreos.com
kind: Installed
metadata:
  labels:
    # Names of related resources
    operators.coreos.com/sub: etcd
    operators.coreos.com/csv: etcdoperator.v0.9.4-clusterwide
    # TODO: Project labels from CSV/Subscription?
  # Named after the package the installed operator belongs to
  name: etcdoperator.v0.9.4-clusterwide
  namespace: local
status:
    # Entire Subscription responsible for installation
    subscription:
        apiVersion: operators.coreos.com/v1alpha1
        kind: Subscription
        metadata:
            name: etcd
            namespace: local-operators
        spec:
            # <subscription spec>
        status:
            # <subscription status>

    # Sanitized CSV responsible for installation
    clusterServiceVersion:
        apiVersion: operators.coreos.com/v1alpha1
        kind: ClusterServiceVersion
        metadata:
            annotations:
                # Annotations are sanitized to remove sensitive info such as `olm.targetNamespaces`
                tectonic-visibility: ocs
                repository: 'https://github.com/coreos/etcd-operator'
                alm-examples: |
                    # <alm examples>
                capabilities: Full Lifecycle
                olm.operatorNamespace: operators
                containerImage: >-
                    quay.io/coreos/etcd-operator@sha256:66a37fd61a06a43969854ee6d3e21087a98b93838e284a6086b13917f96b0d9b
                createdAt: '2019-02-28 01:03:00'
                categories: Database
                description: Create and maintain highly-available etcd clusters on Kubernetes
                olm.operatorGroup: global-operators
            labels:
                olm.api.2c1e6f7e17c07035: provided
                olm.api.2fdc3540750c4d2b: provided
                olm.api.c571d720f17289d3: provided
            name: etcdoperator.v0.9.4-clusterwide
            namespace: local-operators
        spec:
            # <csv spec>
        status:
            # <csv status>
```

## User Experience

### Use Cases

1. Provide information about operators installed and configured to watch a given namespace.

    A client can query for the set of installed operators by listing `installed.packages.operators.coreos.com` in a namespace.

    Ex. Using `kubectl` to list installed operators.

    ```sh
    # Two operators are configured to watch namespace default, etcd and prometheus, which are installed in namespaces operators and monitoring respectively
    $ kubectl -n default get installed.packages.operators.coreos.com
    NAME                              INSTALLATION_NAMESPACE   CHANNEL             CURRENT_VERSION   TARGET_VERSION   PHASE
    etcdoperator.v0.9.4-clusterwide   operators                clusterwide-alpha   0.9.4             0.9.4            Succeeded
    prometheus                        monitoring               beta                0.27.0            0.27.0           Succeeded

    # No operators are configured to watch namespace mojave
    $ kubectl -n default get installed.packages.operators.coreos.com
    No resources found.
    ```

2. Watching changes to the list of installed operators.

    Ex. Using `kubectl` to open a watch on installed operators.

    ```sh
    # An installed operator in namespace default transitions from Succeeded to Failed
    $ kubectl -n default get installed.packages.operators.coreos.com -w
    NAME                              INSTALLATION_NAMESPACE   CHANNEL             CURRENT_VERSION   TARGET_VERSION   PHASE
    etcdoperator.v0.9.4-clusterwide   operators                clusterwide-alpha   0.9.4             0.9.4            Succeeded
    etcdoperator.v0.9.4-clusterwide   operators                clusterwide-alpha   0.9.4             0.9.4            Failed
    ```

### Non-Use Cases

1. External[^1] clients may not create, delete, or mutate `installed.packages.operators.coreos.com` resources.

    Ex. Unable to create an `Installed` resource with `kubectl`

    ```sh
    # Attempt to create Installed resource in namespace mojave
    $ cat <<EOF | kubectl -n mojave create -f -
    apiVersion: v1alpha2/packages.operators.coreos.com
    kind: Installed
    # <continued resource definition>
    EOF
    Error from server (MethodNotAllowed): error when creating "STDIN": the server does not allow this method on the requested resource
    ```

[^1]: Clients external to the API implementation (e.g. not part of the controller responsible for them).

### Supported HTTP Verbs

`installed.packages.operators.coreos.com` HTTP verb support table:

|   Verb   | Supported |
|:--------:|:---------:|
|  `GET`   |     ✔     |
|  `PUT`   |     ❌     |
|  `POST`  |     ❌     |
| `PATCH`  |     ❌     |
| `DELETE` |     ❌     |

## Implementation

### Aggregated API

To circumvent the overhead of managing copies of persistent resources in each namespace, like `PackageManifests`, `Installed` resources will be _synthesized_ on request by an extension API server . The server will watch tracked resources and cache generated `Installed` instances for each operator (`CSV`) on the cluster. When a `GET` is received, the server will retrieve the cached instances configured to watch the request namespace and set `metadata.namespace` to match it before returning the results.

The `Installed` resource belongs to the same API group (`packages.operators.coreos.com`) as `PackageManifest`, but at different versions (`v2alpha1` and `v1` respectively). It follows that they should share the internal types and conversion logic, which makes `packageserver` an ideal place to add this API. However, due to [current limitations in OLM](https://github.com/operator-framework/operator-lifecycle-manager/issues/727) a separate `Deployment` must be introduced into the `packageserver` `CSV` to support the new version (`v2alpha1`). To this end, the names of each deployment will be of the format `packageserver.<version>` and will be referenced in their respective `owned` entry:

```yaml
# Packageserver CSV spec
apiservicedefinitions:
    owned:
    - group: packages.operators.coreos.com
      version: v1
      kind: PackageManifest
      name: packagemanifests
      displayName: PackageManifest
      description: A PackageManifest is a resource generated from existing CatalogSources and their ConfigMaps
      deploymentName: packageserver.v1
      containerPort: 5443
    - group: packages.operators.coreos.com
      version: v2alpha1
      kind: Installed
      name: installed
      displayName: Installed
      description: Installed is a resource generated from installed operator CSVs and their Subscriptions
      deploymentName: packageserver.v2alpha1
      containerPort: 5443
```

### Versioned APIs and Internal Types

To support future API enhancement, both versioned and internal types will be added for the `Installed` resource.

The versioned API structure under package `github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators/v2alpha1`:

```go
package v2alpha1

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v2alpha1"
)

// Versioned API structure for external clients

type Installed struct {
    metav1.TypeMeta `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Status InstalledStatus `json:"status,omitempty"`
}

type InstalledStatus struct {
    // Use specific versions of tracked resources (v1alpha1)
    Subscription v1alpha1.Subscription `json:"omitempty,subscription"`
    ClusterServiceVersion v1alpha1.ClusterServiceVersion `json:"omitempty,clusterServiceVersion"`
}

// <definition of Installed list structure>
```

The internal type under package `github.com/operator-framework/operator-lifecycle-manager/pkg/package-server/apis/operators`:

```go
package operators

import (
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
)

// Internal type for apiserver use

type Installed struct {
    metav1.TypeMeta
    metav1.ObjectMeta

    Status InstalledStatus
}

type InstalledStatus struct {
    // Lock-down specific versions here since they are part of a different API
    Subscription v1alpha1.Subscription
    ClusterServiceVersion v1alpha1.ClusterServiceVersion
}

// <definition of Installed list structure>
```

Conversions, defaults, open-api definitions, informers, clients, etc. will be autogenerated for these types using the [k8s code-generators](https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/code-generator/README.md#code-generator). The set up should be a fairly simple extension of what currently exists for other `packageserver` types.

The extension api-server will use the internal types, clients, and informers for the API group it owns (`packages.operators.coreos.com`).

### Watching

To support watching, `InstalledList` resources will be assigned a `ResourceVersion` field that is an aggregate of those found in its tracked `<Resource>List`s. The aggregate field will be a comma delimited string of tuples in the format `R0:RV0,R1:RV1,...,Ri:RVi,...,Rn:RVn`, where each `Ri` represents a unique, tracked, API list resource, and `RVi` represents its respective `ResourceVersion`. When a watch is requested, the extension api-server will parse the `ResourceVersion` into its components and open a watch for each API resource. When an event is received from one of these watches, the corresponding `Installed` resources will be `resynthesized` and emitted to the top-level `Installed` watch.

### Deprecating Copied `CSV`s

In the long-term, all logic generating copied `CSV`s will _eventually_ be removed.

Changes removing copied `CSV`s from OLM:

- Remove `CSV` copying during `OperatorGroup` reconciliation
- Remove copied `CSV` queues, sync functions, and garbage collection
- Update garbage collection for target namespace RBAC (can no longer add `OwnerReference`s to target `CSV`s)
  - Add surrogate resource in target namespace to use for `OwnerReference` and garbage collection

Changes that support upgrading from OLM version with copied `CSV`s:

- Whenever a copied `CSV` is encountered:
  - Create surrogate object in target namespace
  - Add surrogate `OwnerReference` to RBAC resources owned by the copied `CSV`
  - Delete the copied CSV

This upgrade logic should be removed in the release _after_ the one it is added in.
Note: The upgrade logic could potentially run as an init container instead of being part of OLM's reconciliation.

In the short-term, copied `CSV`s will need to remain until openshift/console has migrated to the `Installed` resource.
