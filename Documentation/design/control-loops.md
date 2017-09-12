# Reconciliation Loops

## Service Catalog
#### `AppType` Loop
1. Watches for new `AppType` defintions in a namespace
    1. Finds the latest`OperatorVersion` for the `AppType` in the catalog.
    1. Creates the `OperatorVersion` in the namespace.

### `OperatorVersion` Loop
1. Watches for pending `OperatorVersion`
    1. If it has a requirement on a `CRD` that doesn't exist, looks it up and creates it in the namespace

### `CRD` loop
1. Watches CRDs for definitions that have `ownerReference` set to `<ALM managed resource>`
    1. Queries catalog by `(group, kind, apiVersion)` for the `AppType` that lists an `OperatorVersion` that has the CRD as a requirement.
    1. If the `AppType` does not exist in the cluster, it is created.

### Catalog loop
1. Finds all `OperatorVersion`s with a higher version and a `replaces` field that includes an existing `OperatorVersion`'s version.
    1. If found, creates the new `OperatorVersion` in the namespace.

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