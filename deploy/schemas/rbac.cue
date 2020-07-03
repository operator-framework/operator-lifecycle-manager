package manifests

import (
    corev1 "k8s.io/api/core/v1"
    rbacv1 "k8s.io/api/rbac/v1"
)

#ServiceAccount: corev1.#ServiceAccount & {
    apiVersion: "v1"
    kind: "ServiceAccount"
    ...
}

#ClusterRole: rbacv1.#ClusterRole & {
    apiVersion: "\(rbacv1.#GroupName)/v1"
    kind: "ClusterRole"
    ...
}

#ClusterRoleBinding: rbacv1.#ClusterRoleBinding & {
    apiVersion: "\(rbacv1.#GroupName)/v1"
    kind: "ClusterRoleBinding"
    ...
}


#OLMServiceAccount: #ServiceAccount & {
    _config: {...}
    metadata: {
        name: _config.serviceAccountName
        namespace: _config.deployNamespace
    }
}

#OLMClusterRole: #ClusterRole & {
    _config: {...}
    metadata: name: "system:controller:operator-lifecycle-manager"
    rules: [
        {
            apiGroups: ["*"]
            resources: ["*"]
            verbs: ["*"]
        },
        {
            nonResourceURLs: ["*"]
            verbs: ["*"]
        }
    ]
}

#OLMClusterRoleBinding: #ClusterRoleBinding & {
    _config: {...}
    metadata: name: "system:controller:operator-lifecycle-manager"
    roleRef: {
        apiGroup: "\(rbacv1.#GroupName)"
        kind: "ClusterRole"
        name: #OLMClusterRole.metadata.name
    }
    subjects: [
        {
            kind: "ServiceAccount"
            name: _config.serviceAccountName
            namespace: _config.deployNamespace
        }
    ]
}
