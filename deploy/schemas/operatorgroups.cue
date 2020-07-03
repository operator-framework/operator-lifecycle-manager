package manifests

import (
    operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
)

#OperatorGroup: operatorsv1.#OperatorGroup & {
    apiVersion: "operators.coreos.com/v1"
    kind: "OperatorGroup"
    ...
}

#GlobalOperatorGroup: #OperatorGroup & {
    _config: {...}
    metadata: {
        name: "global-operators"
        namespace: _config.operatorNamespace
    }
}

#OLMOperatorGroup: #OperatorGroup & {
    _config: {...}
    metadata: {
        name: "olm-operators"
        namespace: _config.deployNamespace
    }
    spec: targetNamespaces: ["openshift-operator-lifecycle-manager"]
}
