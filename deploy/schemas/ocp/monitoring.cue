package manifests

import (
    rbacv1 "k8s.io/api/rbac/v1"
)

#Role: rbacv1.#Role & {
    apiVersion: "\(rbacv1.#GroupName)/v1"
    kind: "Role"
    ...
}

#RoleBinding: rbacv1.#RoleBinding & {
    apiVersion: "\(rbacv1.#GroupName)/v1"
    kind: "RoleBinding"
    ...
}

#MetricsRole: #Role & {
    _config: {...}
    metadata: {
        name: "operator-lifecycle-manager-metrics"
        namespace: _config.deployNamespace
    }
    rules: [{
        apiGroups: ["\"\""]
        resources: ["services", "endpoints", "pods"]
        verbs: ["get", "list", "watch"]
    }]
}

#MetricsRoleBinding: #RoleBinding & {
    _config: {...}
    metadata: {
        name: "operator-lifecycle-manager-metrics"
        namespace: _config.deployNamespace
    }
    roleRef: {
        apiGroup: "\(rbacv1.#GroupName)"
        kind: "Role"
        name: #MetricsRole.metadata.name
    }
    subjects: [{
        kind: "ServiceAccount"
        name: "prometheus-k8s"
        namespace: "openshift-monitoring"
    }]
}

#ServiceMonitor: {
    apiVersion: "monitoring.coreos.com/v1"
    kind: "ServiceMonitor"
    ...
}

#CommomnServiceMonitor: #ServiceMonitor & {
    _config: {...}
    metadata: {
        namespace: _config.deployNamespace
        name: string
    }
    labels: app: metadata.name
    spec: {
        jobLabel: "component"
        namespaceSelector: matchNames: [_config.deployNamespace]
        selector: matchLabels: app: metadata.name
        _endpoint: {
            bearerTokenFile: "/var/run/secrets/kubernetes.io/serviceaccount/token"
            interval: "30s"
            metricRelabelings: [{action: "drop"}]
            regex: "etcd_(debugging|disk|request|server).*"
            sourceLabels: ["__name__"]
            port: "https-metrics"
            scheme: "https"
            tlsConfig: {
                caFile: "/etc/prometheus/configmaps/serving-certs-ca-bundle/service-ca.crt"
                serverName: string
            }
        }
        endpoints: [_endpoint]
    }
}

#OLMServiceMonitor: #CommomnServiceMonitor & {
    _config: {...}
    metadata: name: "olm-operator"
    spec: _endpoint: tlsConfig: serverName: "olm-operator-metrics.openshift-operator-lifecycle-manager.svc"
}

#CatalogServiceMonitor: #CommomnServiceMonitor & {
    _config: {...}
    metadata: name: "catalog-operator"
    spec: _endpoint: tlsConfig: serverName: "catalog-operator-metrics.openshift-operator-lifecycle-manager.svc"
}