package manifests

import (
    corev1 "k8s.io/api/core/v1"
)

// config values are interpolated throughout the manifests
#DefaultConfig: {
    version: "0.0.0"
    debug: *false | bool
    deployNamespace: string
    operatorNamespace: string
    globalCatalogNamespace: *deployNamespace | string
    watchedNamespaces: *"" | string
    serviceAccountName: *"olm-operator" | string
    replicas: *1 | int
    priorityClassName: *"" | string
    pullPolicy: "Always" | "Never" | *"IfNotPresent"
    utilImage: *catalog.imageRef | string
    registryImage: *"quay.io/operator-framework/configmap-operator-registry:latest" | string
    olm: #DefaultContainerConfig & {
        imageRef: *"quay.io/operator-framework/olm:latest" | string
        ...
    }
    catalog: #DefaultContainerConfig & {
        imageRef: *"quay.io/operator-framework/olm:latest" | string
        ...
    }
    packageserver: #DefaultContainerConfig & {
        imageRef: *"quay.io/operator-framework/olm:latest" | string
        ...
    }
    ...
}

#DefaultContainerConfig: {
    commandArgs: *[] | [string]
    imageRef: string
    resources: {
        requests: {
            cpu: "10m"
            memory: "160Mi"
        }
        ...
    }
    nodeSelector: {
        "kubernetes.io/os": "linux"
        ...
    }
    tolerations: [...corev1.#Toleration]
    ...
}