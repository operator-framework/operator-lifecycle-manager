# Reconciliation Loops

## Service Catalog

### `InstallDeclaration` loop

Strawpeople:

```
declare: 
 - Vault AppType 1.0.0 (ref by sha?)
approval: manual/automatic
status: Unresolved
```

```
declare: 
 - Vault AppType (ref by sha?)
approval: manual/automatic
resolved:
  - Vault AppType 1.0.0
  - Vault OpVer
  - VaultService CRD
  - Etcd AppType
  - Etcd OpVer
  - EtcdCluster CRD
status: resolved
```

States: `Unresolved`, `Resolved`, `Approved`, `Complete`

1. Watches for new `InstallDeclarations` in a namespace
    1. If `Unresolved`, attempt to resolve those resources and write them back to the `InstallDeclaration`
    1. If `Resolved`, wait for state to be `Approved`
      1. If `approval` is set to `automatic`, state is transitioned to `Approved`
    1. If `Approved`, creates all resolved resources, reports back status
    1. If `Complete`, nothing

### `Subscription` loop

```
declare: 
 - Vault Apptype 
channel: quay.io/apptypes/vault:stable
approval: manual/automatic
status:
  current: v1.0.0
```

1. Watches for `Subscription` objects
   1. If no `InstallDeclaration` exists for the `Subscription`, creates it
   1. Checks channel source for updates
     1. If newer version is available in the channel and is greater that `current`, creates an `InstallDeclaration` for it.

## ALM

### `OperatorVersion` Install loop
1. Watches for new (no older versions exist) `OperatorVersion` definitions in a namespace
    1. Checks that requirements are met
    1. If requirements are met:
        1. Follows `installStrategy` to install operator into the namespace
    1. Writes status back to `OperatorVersion` about missing requirements or successful deployment


### `OperatorVersion` Upgrade loop

`ownerReference` array docs:
> List of objects depended by this object. If ALL objects in the list have been deleted, this object will be garbage collected. If this object is managed by a controller, then an entry in this list will point to this controller, with the controller field set to true. There cannot be more than one managing controller.

1. Watches for new `OperatorVersion` definitions in a namespace that have a `replaces` field that matches a current running `OperatorVersion`
    1. Follows the normal `install` loop from above to create the new `OperatorVersion`
    1. The new operator should have its own "adoption" loop:
        1. Finds all resources owned by the old operator
        1. Performs any necessary domain-specific handoff, if any
        1. Adds an additional `ownerReferences` entry pointing to the new operator
        1. Sets the `controller` flag to `true` on the `ownerReference` pointing to the new operator
        1. Removes the `ownerReference` pointing to the the old operator

### `OperatorVersion` GC loop

1. Watches for any `OperatorVersion` which owns no resources (via label query) and for which there exists another `OperatorVersion` with a `replaces` field that describes it.
    1. Deletes it
