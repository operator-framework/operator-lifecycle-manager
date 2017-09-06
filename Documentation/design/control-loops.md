### ALM Operator Reconciliation Loops

1. Watches for new AppType definitions and installs defined operators and CRDs.
1. Watches CRDs for new definitions that have `ownerReference` set to ALM.
    1. Queries catalog for the highest version AppType that lists the CRD as an instance.
    1. Installs AppType, if found.
    1. If no AppType exists in the cluster (installed manually or discovered), status is written back to the CRD about the failure.
1. Watches CustomResources (instances of CRDs) that it has an AppType installed for.
    1. If operator is not yet installed, installs operator according to the install strategy for the AppType (operator field)
1. Tracks catalog for new AppType versions higher than those installed.
    1. If all resources managed by the current AppType are also managed by the new AppType, the new AppType can be installed.
        1. If auto-update is enabled, the AppType will be installed in the cluster and the new operator/CRDs will be installed. 
        1. If manual update is enabled, the AppType will be available for installation in the UI.
    1. If there are resources managed by the current AppType that are not managed by the new AppType, the new AppType is not available for installation.
        1. If auto-update is enabled, this results in a cluster alert.
        1. If manual updated is enabled, the AppType will be visible in the UI but not available for installation. 
        1. In all cases, steps can be communicated to the user on how to enable the update to proceed.
        1. Note that this will only be a problem when the `resource` definitions deprecate a version of a CRD, which should correspond to major version changes in the operator.
