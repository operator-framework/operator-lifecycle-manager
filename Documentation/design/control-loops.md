# Reconciliation Loops

## Service Catalog

### `InstallPlan` loop

Strawpeople:

```
declare:
 - Vault ClusterServiceVersion 1.0.0 (ref by sha?)
approval: manual/automatic
status: Unresolved
```

```
declare:
 - Vault ClusterServiceVersion (ref by sha?)
approval: manual/automatic
resolved:
  - Vault ClusterServiceVersion 1.0.0
  - Vault ClusterServiceVersion
  - VaultService CRD
  - Etcd ClusterServiceVersion
  - Etcd ClusterServiceVersion
  - EtcdCluster CRD
status: resolved
```

States: `Unresolved`, `Resolved`, `Approved`, `Complete`

1. Watches for new `InstallPlans` in a namespace
    1. If `Unresolved`, attempt to resolve those resources and write them back to the `InstallPlan`
    1. If `Resolved`, wait for state to be `Approved`
      1. If `approval` is set to `automatic`, state is transitioned to `Approved`
    1. If `Approved`, creates all resolved resources, reports back status
    1. If `Complete`, nothing

### `Subscription` loop

```
type: Subscription
declare:
 - Vault ClusterServiceVersion
source: quay
package: vault
channel: stable
approval: manual/automatic
status:
  current: v1.0.0
---
type: CatalogSource
url: quay.io/catalog
name: quay
```

1. Watches for `Subscription` objects
   1. If no `InstallPlan` exists for the `Subscription`, creates it
   1. Checks `CatalogSource` for updates
     1. If newer version is available in the channel and is greater than `current`, creates an `InstallPlan` for it.

## ALM

### `ClusterServiceVersion` Install loop
1. Watches for new (no older versions exist) `ClusterServiceVersion` definitions in a namespace
    1. Checks that requirements are met
    1. If requirements are met:
        1. Follows `installStrategy` to install operator into the namespace
    1. Writes status back to `ClusterServiceVersion` about missing requirements or successful deployment


### `ClusterServiceVersion` Upgrade loop

`ownerReference` array docs:
> List of objects depended by this object. If ALL objects in the list have been deleted, this object will be garbage collected. If this object is managed by a controller, then an entry in this list will point to this controller, with the controller field set to true. There cannot be more than one managing controller.

1. Watches for new `ClusterServiceVersion` definitions in a namespace that have a `replaces` field that matches a current running `ClusterServiceVersion`
    1. Follows the normal `install` loop from above to create the new `ClusterServiceVersion`
    1. The new operator should have its own "adoption" loop:
        1. Finds all resources owned by the old operator
        1. Performs any necessary domain-specific handoff, if any
        1. Adds an additional `ownerReferences` entry pointing to the new operator
        1. Sets the `controller` flag to `true` on the `ownerReference` pointing to the new operator
        1. Removes the `ownerReference` pointing to the the old operator

### `ClusterServiceVersion` GC loop

1. Watches for any `ClusterServiceVersion` which owns no resources (via label query) and for which there exists another `ClusterServiceVersion` with a `replaces` field that describes it.
    1. Deletes it
