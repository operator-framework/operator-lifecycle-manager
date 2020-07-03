
package manifests

import (
    "list"

    appsv1 "k8s.io/api/apps/v1"
    corev1 "k8s.io/api/core/v1"
)

#Deployment: appsv1.#Deployment & {
    _config: {...}
    apiVersion: "apps/v1"
    kind: "Deployment"
    spec: #DeploymentSpec & {
        _config: _config
        ...
    }
    ...
}


#DeploymentSpec: appsv1.#DeploymentSpec & {
    _config: {...}
    // construct container entry for each _container named field
    _containers: [containerName=string]: corev1.#Container & {name: containerName}
    template: spec: containers: [for k, v in _containers {
        v & {
            // construct ports for every container._ports
            _ports: [portname=string]: corev1.#ContainerPort & {name: portname}
            ports: [for p in _ports {p}] 

            // construct args list
            _args: [argname=string]: [...string]
            if len(_args) > 0 {
            args: list.FlattenN([for k, v in _args {v}], 1)
            }

            // construct env list
            _env: [envname=string]: corev1.#EnvVar & {name: envname}
            if len(_env) > 0 {
            env: [for e in _env {e}] 
            }

            // construct volumemounts
            _volumeMounts: [mountname=string]: corev1.#VolumeMount & {name: mountname}
            if len(_volumeMounts) > 0 {
            volumeMounts: [for k, v in _volumeMounts {{name: k} & v}] 
            }
            ...
        }
    }]

    // construct volumes
    _volumes: [volname=string]: corev1.#Volume & {name: volname}
    if len(_volumes) > 0 {
    template: spec: volumes: [for k, v in _volumes {{name: k} & v}] 
    }
    ...
}

#CommonDeployment: #Deployment & {
    _config: {...}
    _labels: {
        app: string
    }
    metadata: {
        labels: _labels
        namespace: _config.deployNamespace
    }
    
    // default values for operator containers
    spec: _containers: [containerName=string]: {
        imagePullPolicy: _config.pullPolicy
        terminationMessagePolicy: "FallbackToLogsOnError"
        _ports: {
            health: {
                containerPort: 8080
                ...
            }
            metrics: {
                containerPort: 8081
                protocol: "TCP"
                ...
            }
            ...
        }
        livenessProbe: httpGet: {
            path: "/healthz"
            port: _ports.health.containerPort
        }
        readinessProbe: httpGet: {
            path: "/healthz"
            port: _ports.health.containerPort
        }

        _args: {
            if _config.debug {
            debug: ["--debug"]
            }
            ...
        }
        _env: {
            OPERATOR_NAMESPACE: { 
                valueFrom: fieldRef: fieldPath: "metadata.namespace"
                ...
            }
            OPERATOR_NAME: {
                value: metadata.name
                ...
            }
            ...
        }
        ...
    }

    spec: {
        strategy: type: "RollingUpdate"
        replicas: _config.replicas
        selector: matchLabels: _labels
        template: metadata: labels: _labels
        template: spec: {
            serviceAccountName: _config.serviceAccountName
            if _config.priorityClassName != "" {
            priorityClassName?: _config.priorityClassName
            }
        }
        ...
    }
    ...
}

#OLMDeployment: #CommonDeployment & {
    _config: {...}
    _labels: app: "olm-operator"
    metadata: name: "olm-operator"
    spec: nodeSelector: _config.olm.nodeSelector
    if len(_config.olm.tolerations) > 0 {
    spec: tolerations: _config.olm.tolerations
    }
    spec: _containers: "olm-operator": {
        command: ["/bin/olm"]
        image: _config.olm.imageRef
        resources: _config.olm.resources

        // args
        _args: {
            namespace: ["--namespace", "$(OPERATOR_NAMESPACE)"]
            if _config.watchedNamespaces != "" {
            watchedNamespaces: ["--watchedNamespaces", _config.watchedNamespaces]
            }
            if len(_config.olm.commandArgs) > 0 {
            commandArgs: _config.olm.commandArgs
            }
            ...
        }
    }
}

#CatalogDeployment: #CommonDeployment & {
    _config: {...}
    _labels: app: "catalog-operator"
    metadata: name: "catalog-operator"
    spec: nodeSelector: _config.catalog.nodeSelector
    if len(_config.catalog.tolerations) > 0 {
    spec: tolerations: _config.catalog.tolerations
    }
    _containers: "catalog-operator": {
        command: ["/bin/catalog"]
        image: _config.catalog.imageRef
        resources: _config.catalog.resources

        // args
        _args: {
            namespace: ["--namespace", _config.globalCatalogNamespace]
            registryImage: ["-configmapServerImage", _config.registryImage]

            utilImage: ["-util-image", _config.utilImage]
            if len(_config.catalog.commandArgs) > 0 {
            commandArgs: _config.catalog.commandArgs
            }
            ...
        }
    }
}