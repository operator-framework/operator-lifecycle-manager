package manifests

import (
    opv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

#ClusterServiceVersion: opv1alpha1.#ClusterServiceVersion & {
    apiVersion: "operators.coreos.com/v1alpha1"
    kind: "ClusterServiceVersion"
    ...
}

#PackageServerCSV: #ClusterServiceVersion & {
    _config: {...}
    metadata: {
        name: "packageserver"
        namespace: _config.deployNamespace
        labels: {
            "olm.version": _config.version
            "olm.clusteroperator.name": "operator-lifecycle-manager-packageserver"
        }
    }
    spec: {
        maturity: "alpha"
        version: _config.version
        displayName: "Package Server"
        description: "Represents an Operator package that is available from a given CatalogSource which will resolve to a ClusterServiceVersion."
        minKubeVersion: "1.11.0"
        keywords: ["packagemanifests", "olm", "packages"]
        maintainers: [{
            name: "Red Hat"
            email: "openshift-operators@redhat.com"
        }]
        provider: name: "Red Hat"
        links: [{
            name: "Package Server"
            url: "https://github.com/operator-framework/operator-lifecycle-manager"
        }]
        installModes: [{
            type: "OwnNamespace"
            supported: true
        }, {
            type: "SingleNamespace"
            supported: true
        }, {
            type: "MultiNamespace"
            supported: true
        }, {
            type: "AllNamespaces"
            supported: true
        }]
    
        install: {
            strategy: "deployment"
            spec: {
                clusterPermissions: [{
                    serviceAccountName: "olm-operator-serviceaccount"
                    rules: [
                    {
                        apiGroups: ["authorization.k8s.io"]
                        resources: ["subjectaccessreviews"]
                        verbs: ["create", "get"]
                    },
                    {
                        apiGroups: ["\"\""]
                        resources: ["configmaps"]
                        verbs: ["get", "list", "watch"]
                    },
                    {
                        apiGroups: ["operators.coreos.com"]
                        resources: ["catalogsources"]
                        verbs: ["get", "list", "watch"]
                    },
                    {
                        apiGroups: ["packages.operators.coreos.com"]
                        resources: ["packagemanifests"]
                        verbs: ["get", "list"]
                    }
                    ]
                }]
                deployments: [{
                    name: "packageserver"
                    spec: #DeploymentSpec & {
                        _labels: app: "packageserver"
                        strategy: type: "RollingUpdate"
                        replicas: 2
                        selector: matchLabels: _labels
                        _containers: packageserver: {
                            command: ["/bin/package-server"]
                            image: _config.packageserver.imageRef
                            imagePullPolicy: _config.pullPolicy
                            resources: _config.packageserver.resources
                            terminationMessagePolicy: "FallbackToLogsOnError"
                            _args: {
                                if _config.debug {
                                debug: ["-v=4"]
                                }
                                port: ["--secure-port", "\(_ports.server.containerPort)"]
                                globalCatalogNamespace: ["--global-namespace", _config.globalCatalogNamespace]
                                ...
                            }
                            _ports: {
                                server: {
                                    containerPort: 5443
                                    ...
                                }
                                ...
                            }
                            livenessProbe: httpGet: {
                                path: "/healthz"
                                port: _ports.server.containerPort
                            }
                            readinessProbe: httpGet: {
                                path: "/healthz"
                                port: _ports.server.containerPort
                            }
                        }
                        template: {
                            metadata: labels: _labels
                            spec: {
                                serviceAccountName: _config.serviceAccountName
                                if _config.priorityClassName != "" {
                                priorityClassName?: _config.priorityClassName
                                }
                                nodeSelector: _config.packageserver.nodeSelector
                                if len(_config.packageserver.tolerations) > 0 {
                                tolerations: _config.packageserver.tolerations
                                 }
                                ...
                            }
                            ...
                        }
                        ...
                    }
                }]
            }
        }
        apiservicedefinitions: owned: [{
            group: "packages.operators.coreos.com"
            version: "v1"
            kind: "PackageManifest"
            name: "packagemanifests"
            displayName: "PackageManifest"
            description: "A PackageManifest is a resource generated from existing CatalogSources which represents operators avaialable for install."
            deploymentName: install.spec.deployments[0].name
            containerPort: install.spec.deployments[0].spec._containers.packageserver._ports.server.containerPort
        }]
    }
}
