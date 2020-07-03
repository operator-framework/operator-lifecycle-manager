package manifests

import (
    opv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

#CatalogSource: opv1alpha1.#CatalogSource & {
    apiVersion: "operators.coreos.com/v1alpha1"
    kind: "CatalogSource"
    ...
}

#OperatorHubCatalogSource: #CatalogSource & {
    _config: {...}
    metadata: {
        name: "operatorhubio-catalog"
        namespace: _config.deployNamespace
    }
    spec:{
        sourceType: "grpc"
        image: "quay.io/operator-framework/upstream-community-operators:latest"
        displayName: "Community Operators"
        publisher: "OperatorHub.io"
    }
}
