package manifests

import (
    corev1 "k8s.io/api/core/v1"
)


#Namespace: corev1.#Namespace & {
    apiVersion: "v1"
    kind: "Namespace"
    ...
}

// DeployNamespace is where OLM operators run
#DeployNamespace: #Namespace & {
    _config: {...}
    metadata: {
        name: _config.deployNamespace
        ...
    }
    ...
}

// OperatorNamespace is the default install location for operators
#OperatorNamespace: #Namespace & {
    _config: {...}
    metadata: {
        name: _config.operatorNamespace
        ...
    }
    ...
}