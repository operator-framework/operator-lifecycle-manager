### ALM Operator Reconciliation Loops

#### `AppType` Loop
1. Watches for new `AppType` defintions in a namespace
    1. Finds the latest `OperatorVersion` for the `AppType` in the catalog.
    1. If the `OperatorVersion` is newer than the installed `OperatorVersion`:
        1. Determines if an automatic upgrade is possible, if so, upgrades `OperatorVersion`
        1. If auto upgrade is not possible, status is written back to the installed `OperatorVersion` about the higher version that's available (but blocked). 
    1. If no `OperatorVersion` is installed for the `AppType`, applies the latest `OperatorVersion` to the cluster.

#### `OperatorVersion` loop
1. Watches for new `OperatorVersion` definitions in a namespace
    1. Checks that requirements are met
    1. If requirements are met:
        1. Follows `installStrategy` to install operator into the namespace
    1. If requirements are not met:
        1. If one of the requirements is a CRD, searches for that CRD by `(group, kind, apiVersion)` and installs it if found. Sets `ownerReference` of the CRD to ALM.
    1. Writes status back to `OperatorVersion` about missing requirements or successful deployment

### `CRD` loop
1. Watches CRDs for definitions that have `ownerReference` set to ALM.
    1. Queries catalog by `(group, kind, apiVersion)` for the `AppType` that lists an `OperatorVersion` that has the CRD as a requirement.
    1. If the `AppType` does not exist in the cluster, it is created.

### Catalog loop
1. Tracks catalog for new `OperatorVersions` higher than those installed.
    1. If the `OperatorVersion` is newer than the installed `OperatorVersion`:
        1. Determines if an automatic upgrade is possible, if so, upgrades `OperatorVersion`
        1. If auto upgrade is not possible, status is written back to the installed `OperatorVersion` about the higher version that's available (but blocked).