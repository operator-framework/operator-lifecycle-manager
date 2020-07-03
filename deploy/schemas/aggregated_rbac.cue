package manifests

#EditAggregatedClusterRole: #ClusterRole & {
    _config: {...}
    metadata: {
        name: "aggregate-olm-edit"
        // Add these permissions to the "admin" and "edit" default roles.
        labels: {
            "rbac.authorization.k8s.io/aggregate-to-admin": "true"
            "rbac.authorization.k8s.io/aggregate-to-edit": "true"
        }
    }
    rules: [
        {
            apiGroups: ["operators.coreos.com"]
            resources: ["subscriptions"]
            verbs: ["create", "update", "patch", "delete"]
        },
        {
            apiGroups: ["operators.coreos.com"]
            resources: ["clusterserviceversions", "catalogsources", "installplans", "subscriptions"]
            verbs: ["delete"]
        }
    ]
}

#ViewAggregatedClusterRole: #ClusterRole & {
    _config: {...}
    metadata: {
        name: "aggregate-olm-view"
        // Add these permissions to the "admin", "edit", and "view" default roles.
        labels: {
            "rbac.authorization.k8s.io/aggregate-to-admin": "true"
            "rbac.authorization.k8s.io/aggregate-to-edit": "true"
            "rbac.authorization.k8s.io/aggregate-to-view": "true"
        }
    }
    rules: [
        {
            apiGroups: ["operators.coreos.com"]
            resources: ["clusterserviceversions", "catalogsources", "installplans", "subscriptions", "operatorgroups"]
            verbs: ["get", "list", "watch"]
        },
        {
            apiGroups: ["packages.operators.coreos.com"]
            resources: ["packagemanifests", "packagemanifests/icon"]
            verbs: ["get", "list", "watch"]
        }
    ]
}