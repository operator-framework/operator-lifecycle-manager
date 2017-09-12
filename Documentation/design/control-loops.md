# Reconciliation Loops

## Service Catalog
#### `AppType` Loop
1. Watches for new `AppType` defintions in a namespace
    1. Finds the latest `OperatorVersion` for the `AppType` in the catalog.
    1. If the `OperatorVersion` is newer than the installed `OperatorVersion`:
        1. Determines if an automatic upgrade is possible, if so, upgrades `OperatorVersion`
        1. If auto upgrade is not possible, status is written back to the installed `OperatorVersion` about the higher version that's available (but blocked). 
    1. If no `OperatorVersion` is installed for the `AppType`, applies the latest `OperatorVersion` to the cluster.

### `CRD` loop
1. Watches CRDs for definitions that have `ownerReference` set to `<ALM managed resource>`
    1. Queries atalog by `(group, kind, apiVersion)` for the `AppType` that lists an `OperatorVersion` that has the CRD as a requirement.
    1. If the `AppType` does not exist in the cluster, it is created.

### Catalog loop
1. Queries catalog for new `OperatorVersion`s with a higher version and a `replaces` range that includes the current version.
    1. If found, creates the new `OperatorVersion` in the namespace.
    1. If the `OperatorVersion` is newer than the installed `OperatorVersion`:
        1. Determines if an automatic upgrade is possible, if so, upgrades `OperatorVersion`
        1. If auto upgrade is not possible, status is written back to the installed `OperatorVersion` about the higher version that's available (but blocked).

## ALM

### `OperatorVersion` Install loop
1. Watches for new (no older versions exist) `OperatorVersion` definitions in a namespace
    1. Checks that requirements are met
    1. If requirements are met:
        1. Follows `installStrategy` to install operator into the namespace
    1. If requirements are not met:
        1. If one of the requirements is a CRD, searches for `(CRD, AppType)` by `(group, kind, apiVersion)` and installs them if found. 
            1. Sets `ownerReference` of the CRD to the AppType.
    1. Writes status back to `OperatorVersion` about missing requirements or successful deployment


### `OperatorVersion` Upgrade loop

`ownerReference` array docs:
> List of objects depended by this object. If ALL objects in the list have been deleted, this object will be garbage collected. If this object is managed by a controller, then an entry in this list will point to this controller, with the controller field set to true. There cannot be more than one managing controller.

Option 1 (ALM managed upgrade):

1. Watches for new `OperatorVersion` definitions in a namespace that have a `replaces` field that matches a current running `OperatorVersion`
    1. Follows the normal `install` loop from above to create the new `OperatorVersion`
    1. Uses the label selector defined on the old `OperatorVersion` to find all resources managed by the old operator.
    1. For each of those resources:
        1. Adds an additional `ownerReferences` entry to the new operator
        1. The `controller` flag on the new reference is set to `false`
    1. The new operator should have its own "adoption" loop:
        1. Finds all resources owned by the old operator and itself
        1. Performs any necessary domain-specific handoff, if any
        1. Sets the `controller` flag to `true` on the `ownerReference` pointing to th enew operator
        1. Removes the `ownerReference` pointing to the the old operator

Option 2 (Operator managed upgrade)

1. Watches for new `OperatorVersion` definitions in a namespace that have a `replaces` field that matches a current running `OperatorVersion`
    1. Follows the normal `install` loop from above to create the new `OperatorVersion`
    1. The new operator should have its own "adoption" loop:
        1. Finds all resources owned by the old operator
        1. Performs any necessary domain-specific handoff, if any
        1. Adds an additional `ownerReferences` entry pointing to the new operator
        1. Sets the `controller` flag to `true` on the `ownerReference` pointing to th enew operator
        1. Removes the `ownerReference` pointing to the the old operator

### `OperatorVersion` GC loop

1. Watches for any `OperatorVersion` which owns no resources (via label query) and for which there exists another `OperatorVersion` with a `replaces` field that describes it.
    1. Deletes it