package manifests

#ClusterOperator: {
    apiVersion: "config.openshift.io/v1"
    kind: "ClusterOperator"
    status: versions: [{
        name: "operator"
        version: "0.0.1-snapshot"
    }]
    metadata: name: string
}

#OLMClusterOperator: #ClusterOperator & {
    metadata: name: "operator-lifecycle-manager"
}

#CatalogClusterOperator: #ClusterOperator & {
    metadata: name: "operator-lifecycle-manager-catalog"
}

#PackageServerClusterOperator: #ClusterOperator & {
    metadata: name: "operator-lifecycle-manager-packageserver"
}
