package manifests

#OCPNamespaceMeta: {
    metadata: {
        annotations: {
            "openshift.io/node-selector": ""
        }
        labels: {
            "openshift.io/scc": "anyuid"
            "openshift.io/cluster-monitoring": "true"
        }
        ...
    }
    ...
}

DeployNamespace: #DeployNamespace & #OCPNamespaceMeta

OperatorNamespace: #OperatorNamespace & #OCPNamespaceMeta


