# Install Guide

## Prereqs

 - Kubernetes 1.8 Cluster
 - Kubectl configured to talk to it

## Install ALM Types


### Install AppType

```sh
kubectl create -f design/resources/apptype.crd.yaml
```

### Install OperatorVersion

```sh
kubectl create -f design/resources/operatorversion.crd.yaml
kubectl create -f design/resources/almoperator.operatorversion.yaml
```

## Using ALM Types

### Install an AppType

```sh
$ kubectl create -f design/resources/samples/etcd/etcd.apptype.yaml
$ kubectl get apptype-v1s
NAME      KIND
etcd      AppType-v1.v1alpha1.app.coreos.com
```

### Install an OperatorVersion

```sh
$ kubectl create -f design/resources/samples/etcd/etcdoperator.operatorversion.yaml
$ kubectl get operatorversion-v1s
NAME                   KIND
alm-operator.0.0.1     OperatorVersion-v1.v1alpha1.app.coreos.com
etcd-operator.v0.5.1   OperatorVersion-v1.v1alpha1.app.coreos.com
```

### Install samples and query for related resources
```sh
$ kubectl apply -f design/resources/samples/etcd
$ kubectl apply -f design/resources/samples/vault
```

Get all EtcClusters associated with the Etcd AppType

```sh
$ kubectl get etcdclusters -l $(kubectl get apptype-v1s etcd -o=json | jq -j '.spec.selector.matchLabels | to_entries | .[] | "\(.key)=\(.value),"' | rev | cut -c 2- | rev)
``` 

Find all CRDs associated with an AppType:
```sh
$ kubectl get customresourcedefinitions -l $(kubectl get apptype-v1s etcd -o=json | jq -j '.spec.selector.matchLabels | to_entries | .[] | "\(.key)=\(.value),"' | rev | cut -c 2- | rev)
```

For each CRD associated with an AppType, find all instances:
```sh
sel=$(kubectl get apptype-v1s etcd -o=json | jq -j '.spec.selector.matchLabels | to_entries | .[] | "\(.key)=\(.value),"' | rev | cut -c 2- | rev) 
crds=$(kubectl get customresourcedefinitions -l $sel -o json | jq -r '.items[].spec.names.plural')

echo $crds | while read crd; do
    echo "$crd"
    kubectl get $crd -l $sel
done
```

Find the outputs for a CRD:

```sh
$ kubectl get customresourcedefinitions etcdclusters.etcd.database.coreos.com -o jsonpath='{.metadata.annotations.outputs}' | jq
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
