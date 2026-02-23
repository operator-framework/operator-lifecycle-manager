# Resolving Bundle Images

An operator [UpdateGraph](https://operator-framework.github.io/olm-book/docs/glossary.html#upgrade-graph) may refer to an operator [Bundle](https://operator-framework.github.io/olm-book/docs/glossary.html#bundle) with a reference to a [Bundle Image](https://operator-framework.github.io/olm-book/docs/glossary.html#bundle-image) containing its content. This means that the content of these referenced bundles is not immediately available for application to a cluster and must first be pulled and unpacked.

## Resolving

The same metadata available for bundles queried from a [`CatalogSource`](https://operator-framework.github.io/olm-book/docs/glossary.html#catalogsources) is available for bundle images. This lets OLM resolve dependencies and updates without pulling them to the cluster. Once the final set of operators has been identified for install, OLM codifies the information needed to pull any included bundle images in the `status.BundleLookups` field of the resulting `InstallPlan`:

```yaml
status:
  bundleLookups:
  - path: quay.io/coreos/prometheus-operator@sha256...
    replaces: ""
    catalogSourceRef:
      Namespace: operators
      Name: monitoring
```

## Unpacking

OLM uses the `status.bundleLookups` field, added to `InstallPlans` during dependency resolution, to determine which bundle images need to be unpacked.

Given an `InstallPlan` with the following `status`:

```yaml
status:
  bundleLookups:
  - path: quay.io/coreos/prometheus-operator@sha256...
    replaces: ""
    catalogSourceRef:
      Namespace: operators
      Name: monitoring
  - path: quay.io/coreos/etcd-operator@sha256...
    replaces: "etcd-operator.v4.1"
    catalogSourceRef:
      Namespace: operators
      Name: storage
```

__Note:__ Image tag references may be used in place of digests, but once a tag has been unpacked, updates to the underlying image will not be respected unless the resources described below are deleted._

Each unique `BundlePath` will result in OLM creating four top-level resources in the namespace of the referenced `CatalogSource`:

1. A `ConfigMap` to hold the unpacked manifests
2. A `Role` allowing `create`, `get`, and `update` on that `ConfigMap`
3. A `RoleBinding` granting that `Role` to the default `ServiceAccount`
4. An unpack `Job` using the default `ServiceAccount` to export the bundle image's content into that `ConfigMap`

OLM uses the same reproducible name for all of these resources; the `sha256` checksum of the respective `BundlePath`.

__Note:__ _This choice of name allows OLM to reuse previously unpacked bundles between `InstallPlans` by making them discoverable and ensuring resource uniqueness._

The `Role`, `RoleBinding`, and `Job` have `OwnerReferences` to the `ConfigMap`, while the `ConfigMap` has an `OwnerReference` to the `CatalogSource` referenced by its respective `BundleLookup`. If the referenced `CatalogSource` is not found, a `BundleLookupPending` condition is added to the `BundleLookup`:

```yaml
path: quay.io/coreos/prometheus-operator@sha256...
replaces: ""
catalogSourceRef:
  Namespace: operators
  Name: monitoring
conditions:
  type: BundleLookupPending
  status: "True"
  reason: CatalogSourceMissing
  message: "referenced catalogsource not found"
  lastTransitionTime: "2020-01-08T23:42:59Z"
```

A given unpack `Job` will start a `Pod` consisting of two containers:

1. An init container that has a release of the [`opm`](https://github.com/operator-framework/operator-registry/tree/master/cmd/opm) binary
2. A container from the bundle image reference

These two containers share a volume mount into which the init container copies its `opm` binary. After initalization, the bundle image container uses this copy to execute the `opm bundle extract` command, extracting the bundle content from its filesystem into the bundle's respective `ConfigMap`.

When an unpack `Job` exists but is not in a `Complete` state, a `BundleLookupPending` condition is added to its `BundleLookup`:

```yaml
path: quay.io/coreos/prometheus-operator@sha256...
replaces: ""
catalogSourceRef:
  Namespace: operators
  Name: monitoring
conditions:
  type: BundleLookupPending
  status: "True"
  reason: JobIncomplete
  message: "unpack job not completed"
  lastTransitionTime: "2020-01-08T23:43:30Z"
```

Once an unpack `Job` runs to completion, the data in the respective `ConfigMap` is converted into a set of install steps and is added to the status the `InstallPlan`. In the same transaction, the `BundleLookup` entry is removed.
