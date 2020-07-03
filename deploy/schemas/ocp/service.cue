package manifests

import (
    corev1 "k8s.io/api/core/v1"
)

#Service: #Object & corev1.#Service & {
    apiVersion: "v1"
    kind: "Service"
    ...
}

#MetricService: #Service & {
    _config: {...}
    _labels: {
        app: string
    }
    metadata: {
        namespace: _config.deployNamespace
        annotations: {
            "service.alpha.openshift.io/serving-cert-secret-name": string
        }
        labels: _labels
    }
    spec: {
        type: "ClusterIP"
        ports: [
            {
                name: "https-metrics"
                port: 8081
                protocol: "TCP"
                targetPort: "metrics"
            }
        ]
        selector: _labels
    }
}

#OLMMetricService: #MetricService & {
    _config: {...}
    _labels: {
        app: "olm-operator"
    }
    metadata: {
        name: "olm-operator-metrics"
        annotations: {
            "service.alpha.openshift.io/serving-cert-secret-name": "olm-operator-serving-cert"
        }
    }
}

#CatalogMetricService: #MetricService & {
    _config: {...}
    _labels: {
        app: "catalog-operator"
    }
    metadata: {
        name: "catalog-operator-metrics"
        annotations: {
            "service.alpha.openshift.io/serving-cert-secret-name": "catalog-operator-serving-cert"
        }
    }
}
