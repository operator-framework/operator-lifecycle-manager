package manifests

// ocp-flavored config
#DefaultOCPConfig: #DefaultConfig & {
    deployNamespace: "openshift-operator-lifecycle-manager"
    operatorNamespace: "openshift-operators"
    globalCatalogNamespace: "openshift-marketplace"
    priorityClassName: "system-cluster-critical"
    olm: {
        nodeSelector: {
            "node-role.kubernetes.io/master": ""
            ...
        }
        tolerations: [
            {
                effect: "NoSchedule"
                key: "node-role.kubernetes.io/master"
                operator: "Exists"
            },
            {
                effect: "NoExecute"
                key: "node.kubernetes.io/unreachable"
                operator: "Exists"
                tolerationSeconds: 120
            },
            {
                effect: "NoExecute"
                key: "node.kubernetes.io/not-ready"
                operator: "Exists"
                tolerationSeconds: 120
            },
        ]
        ...
    }
    catalog: {
        nodeSelector: {
            "node-role.kubernetes.io/master": ""
            ...
        }
        tolerations: [
            {
                effect: "NoSchedule"
                key: "node-role.kubernetes.io/master"
                operator: "Exists"
            },
            {
                effect: "NoExecute"
                key: "node.kubernetes.io/unreachable"
                operator: "Exists"
                tolerationSeconds: 120
            },
            {
                effect: "NoExecute"
                key: "node.kubernetes.io/not-ready"
                operator: "Exists"
                tolerationSeconds: 120
            },
        ]
        ...
    }
    packageserver: {
        nodeSelector: {
            "node-role.kubernetes.io/master": ""
            ...
        }
        tolerations: [
            {
                effect: "NoSchedule"
                key: "node-role.kubernetes.io/master"
                operator: "Exists"
            },
            {
                effect: "NoExecute"
                key: "node.kubernetes.io/unreachable"
                operator: "Exists"
                tolerationSeconds: 120
            },
            {
                effect: "NoExecute"
                key: "node.kubernetes.io/not-ready"
                operator: "Exists"
                tolerationSeconds: 120
            },
        ]
        ...
    }
    ...
}