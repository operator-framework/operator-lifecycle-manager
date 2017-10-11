# Install Guide

## Prereqs

 - Kubernetes 1.8 Cluster
   - 1.7 will work, but CRs will not be validated against the schema in the corresponding CRD
 - Kubectl configured to talk to it

## Install ALM Types

### Install ALM Namespace

```sh
kubectl create -f <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: alm
EOF
```

### Install ClusterServiceVersion

```sh
kubectl create -f - <<EOF
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  name: clusterserviceversion-v1s.app.coreos.com
  annotations:
    displayName: Operator Version
    description: Represents an Operator that should be running on the cluster, including requirements and install strategy.
spec:
  group: app.coreos.com
  version: v1alpha1
  scope: Namespaced
  validation:
    openAPIV3Schema:
      type: object
      description: Represents a single version of the operator software
      required:
      - displayName
      - version
      - maturity
      - labels
      - selector
      - install
      properties:
        displayName:
          type: string
          description: Human readable name of the application that will be displayed in the ALM UI

        description:
          type: string
          description: Human readable description of what the application does

        keywords:
          type: array
          description: List of keywords which will be used to discover and categorize app types
          items:
            type: string

        maintainers:
          type: array
          description: Those responsible for the creation of this specific app type
          items:
            type: object
            description: Information for a single maintainer
            required:
            - name
            - email
            properties:
              name:
                type: string
                description: Maintainer's name
              email:
                type: string
                description: Maintainer's email address
                format: email
            optionalProperties:
              type: string
              description: "Any additional key-value metadata you wish to expose about the maintainer, e.g. github: <username>"

        links:
          type: array
          description: Interesting links to find more information about the project, such as marketing page, documentation, or github page
          items:
            type: object
            description: A single link to describe one aspect of the project
            required:
            - name
            - url
            properties:
              name:
                type: string
                description: Name of the link type, e.g. homepage or github url
              url:
                type: string
                description: URL to which the link should point
                format: uri

        icon:
          type: array
          description: Icon which should be rendered with the application information
          required:
          - base64data
          - mediatype
          properties:
            base64data:
              type: string
              description: Base64 binary representation of the icon image
              pattern: ^(?:[A-Za-z0-9+/]{4}){0,16250}(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$
            mediatype:
              type: string
              description: Mediatype for the binary data specified in the base64data property
              enum:
              - image/gif
              - image/jpeg
              - image/png
              - image/svg+xml
        version:
          type: string
          description: Version string, recommended that users use semantic versioning
          pattern: ^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-(0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(\.(0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*)?(\+[0-9a-zA-Z-]+(\.[0-9a-zA-Z-]+)*)?$

        replaces:
          type: string
          description: Name of the ClusterServiceVersion custom resource that this version replaces

        maturity:
          type: string
          description: What level of maturity the software has achieved at this version
          enum:
          - planning
          - pre-alpha
          - alpha
          - beta
          - stable
          - mature
          - inactive
          - deprecated
        labels:
          type: object
          description: Labels that will be applied to associated resources created by the operator.
        selector:
          type: object
          description: Label selector to find resources associated with or managed by the operator
          properties:
            matchLabels:
              type: object
              description: Label key:value pairs to match directly
            matchExpressions:
              type: array
              descriptions: A set of expressions to match against the resource.
              items:
                allOf:
                  - type: object
                    required:
                    - key
                    - operator
                    - values
                    properties:
                      key:
                        type: string
                        description: the key to match
                      operator:
                        type: string
                        description: the operator for the expression
                        enum:
                        - In
                        - NotIn
                        - Exists
                        - DoesNotExist
                      values:
                        type: array
                        description: set of values for the expression
        customresourcedefinitions:
          type: object
          properties:
            owned:
              type: array
              description: What resources this operator is responsible for managing. No two running operators should manage the same resource.
              items:
                type: string
                description: Fully qualified name of the CustomResourceDefinition (e.g. my-resource-v1.app.coreos.com)
            required:
              type: array
              description: The CustomResourceDefinitions that are required before this ClusterServiceVersion can run
              items:
                type: string
                description: Fully qualified name of the CustomResourceDefinition (e.g. my-resource-v1.app.coreos.com)

        install:
          type: object
          description: Information required to install this specific version of the operator software
          oneOf:
          - type: object
            required:
            - strategy
            - spec
            properties:
              strategy:
                type: string
                enum: ['image']
              spec:
                type: object
                required:
                - image
                properties:
                  image: string
          - type: object
            required:
            - strategy
            - spec
            properties:
              strategy:
                type: string
                enum: ['deployment']
              spec:
                type: object
                required:
                - deployments
                properties:
                  deployments:
                    type: array
                    description: List of deployments to create
                    items:
                      type: object
                      description: A deployment to create in the cluster

  names:
    plural: clusterserviceversion-v1s
    singular: clusterserviceversion-v1
    kind: ClusterServiceVersion-v1
    listKind: ClusterServiceVersionList-v1
EOF
```

### Install ALM Operator

* Create a pull secret `coreos-pull-secret` that can read `quay.io/coreos/alm`
* Create the service account
```sh
kubectl create -f - <<EOF
kind: ServiceAccount
apiVersion: v1
metadata:
  name: alm-operator-serviceaccount
  namespace: alm
---
apiVersion: rbac.authorization.k8s.io/v1beta1
kind: ClusterRoleBinding
metadata:
  name: alm-operator
  namespace: alm
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: ServiceAccount
  name: alm-operator-serviceaccount
  namespace: default
EOF
```
* Create the deployment
```sh
kubectl create -f -<<EOF
apiVersion: extensions/v1beta1
kind: Deployment
metadata:
  name: alm-operator
  namespace: alm
  labels:
    app: alm-operator
spec:
  strategy:
    type: RollingUpdate
  replicas: 1
  template:
    metadata:
      labels:
        app: alm-operator
    spec:
      serviceAccountName: alm-operator-serviceaccount
      containers:
        - name: alm
          image: quay.io/coreos/alm:latest
          imagePullPolicy: Always
          ports:
            - containerPort: 8080
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
          resources:
      imagePullSecrets:
        - name: coreos-pull-secret
EOF
```

## Using ALM Types

### Install an AppType

```sh
kubectl create -f ../design/resources/samples/etcd/etcd.apptype.yaml
```
```sh
kubectl --namespace=alm get clusterserviceversion-v1s
NAME      KIND
etcd      AppType-v1.v1alpha1.app.coreos.com
```

### Install an ClusterServiceVersion

```sh
kubectl create -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: alm-etcd-example
EOF

kubectl create -f ../design/resources/samples/etcd/etcdoperator.clusterserviceversion.yaml

```
```
kubectl --namespace=alm-etcd-example get clusterserviceversion-v1s
NAME                   KIND
etcd-operator.v0.5.1   ClusterServiceVersion-v1.v1alpha1.app.coreos.com
```

### Install samples and query for related resources

```sh
kubectl apply -f ../design/resources/samples/etcd
```

```sh
kubectl create -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: alm-vault-example
EOF
kubectl apply -f ../design/resources/samples/vault
```

Get all EtcdClusters associated with the Etcd AppType

```sh
$ kubectl get --namespace=alm-etcd-example  etcdclusters -l $(kubectl get clusterserviceversion-v1s etcd -o=json | jq -j '.spec.selector.matchLabels | to_entries | .[] | "\(.key)=\(.value),"' | rev | cut -c 2- | rev)
``` 

Find all CRDs associated with an AppType:
```sh
$ kubectl get --namespace=alm-etcd-example  customresourcedefinitions -l $(kubectl get clusterserviceversion-v1s etcd -o=json | jq -j '.spec.selector.matchLabels | to_entries | .[] | "\(.key)=\(.value),"' | rev | cut -c 2- | rev)
```

For each CRD associated with an AppType, find all instances:
```sh
sel=$(kubectl get --namespace=alm-etcd-example clusterserviceversion-v1s etcd -o=json | jq -j '.spec.selector.matchLabels | to_entries | .[] | "\(.key)=\(.value),"' | rev | cut -c 2- | rev)
crds=$(kubectl get --namespace=alm-etcd-example customresourcedefinitions -l $sel -o json | jq -r '.items[].spec.names.plural')

echo $crds | while read crd; do
    echo "$crd"
    kubectl get --namespace=alm-etcd-example $crd -l $sel
done
```

Find the outputs for a CRD:

```sh
$ kubectl get --namespace=alm-etcd-example customresourcedefinitions etcdclusters.database.coreos.com -o jsonpath='{.metadata.annotations.outputs}' | jq
```
```json
{
  "etcd-cluster-service-name": {
    "displayName": "Service Name",
    "description": "The service name for the running etcd cluster.",
    "x-alm-capabilities": [
      "urn:alm:capability:com.coreos.etcd:api.v3.grpc",
      "urn:alm:capability:com.coreos.etcd:api.v2.rest"
    ]
  },
  "etcd-dashboard": {
    "displayName": "Dashboard",
    "description": "URL of a Grafana dashboard for the etcd cluster.",
    "x-alm-capabilities": [
      "urn:alm:capability:com.tectonic.ui:important.link",
      "urn:alm:capability:org.w3:link"
    ]
  },
  "etcd-prometheus": {
    "displayName": "Prometheus Endpoint",
    "description": "Endpoint of the prometheus instance for the etcd cluster.",
    "x-alm-capabilities": [
      "urn:alm:capability:io.prometheus:prometheus.v1",
      "urn:alm:capability:org.w3:link"
    ]
  },
  "etcd-important-metrics": {
    "displayName": "Important Metrics",
    "description": "Important prometheus metrics for the etcd cluster.",
    "x-alm-capabilities": [
      "urn:alm:capability:com.tectonic.ui:metrics"
    ]
  }
}
```
