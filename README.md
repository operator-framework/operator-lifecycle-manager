# Application Lifecycle Manager

## Goals

The aim of this project is to serve the following purposes:

* CRUD operations on top level service/app types
* CRUD operations on service/app instances
* Answer forensic questions and generate audit logs about state transitions for the above
* To do so in the most kubernetes native way possible

## Usage

**Asumption**: The ALM operator is already installed in the cluster, which defines an app CRD.

```sh
kubectl get crd
NAME                          KIND
app.stable.coreos.com         CustomResourceDefinition.v1beta1.apiextensions.k8s.io
```

### Create an instance of an app type

```sh
kubectl create -f etcdapp.yaml
```

```yaml
apiVersion: app.coreos.com/v1beta1
kind: AppType
metadata:
  # (name, version, type) is unique and immutable
  name: etcd
  version: 1  
  type: com.tectonic.storage
spec:
  operator:
    # an install strategy for the operator.
    type: helm
    helmChart:
      template: quay.io/coreos/etcd-operator@sha256:dc2jk
      values:
        replicas: 2
  resources:
  # A resource is a CRD that the operator watches, along with definitions of outputs
  - name: etcds.v1alpha1.etcd.coreos.com
    spec:
      group: v1alpha1.etcd.coreos.com
      version: v1alpha1
      scope: Namespaced
      names:
        plural: etcds
        singular: etcd
        kind: EtcdCluster
      validation:
        openAPIv3: |
          {
            // real json schema
            "size": "int",
            "version": "string",
            "autoMinorVersionUpgrade": "bool"
          } 
    outputs:
    # These should be added as status fields to EtcdCluster CR by the etcd operator
    - name: service-name
      type: string
      description: The service name at which to contact the newly formed etcd
```

### List the app types

```sh
kubectl get Apps
NAME                 KIND
etcd                 Etcd.v1alpha1.etcd.coreos.com
```

### Create an instance of the managed CRD

```sh
kubectl create -f myetcd.yaml
```

```yaml
apiVersion: v1alpha1.etcd.coreos.com/v1alpha1
kind: EtcdCluster
metadata:
  name: etcd-purple-ant
  namespace: default
spec:
  size: 3
  version: 3.2.2
  autoMinorVersionUpgrade: True
```

### List instances of the app

```sh
kubectl get etcds
NAME                 KIND
etcd-purple-ant      Etcd.v1alpha1.etcd.coreos.com
```

### Get details about the app

```sh
kubectl get etcd etcd-purple-ant -o yaml
```

```yaml
apiVersion: v1alpha1.etcd.coreos.com/v1
kind: EtcdCluster
metadata:
  creationTimestamp: 2017-07-20T23:52:44Z
  name: etcd-purple-ant
  namespace: default
  resourceVersion: "6964"
  selfLink: /apis/etcd.coreos.com/v1/namespaces/default/etcds/etcd-purple-ant
  uid: 8657f388-6da6-11e7-8ab2-08002787f71a
spec:
  size: 3
  version: 3.2.2
  autoMinorVersionUpgrade: True
status:
  outputs:
    service-name: etcd-purple-ant.service
    client-cert-secret: etcd-purple-ant-cert
    client-cert-key-secret: etcd-purple-ant-key
```

### Simple deployment of a stateless app

```sh
kubectl create -f coreoswebsite.yaml
```

```yaml
apiVersion: app.coreos.com/v1beta1
kind: AppType
metadata:
  name: coreos-website
  type: com.tectonic.web
spec:
  operator:
    type: helm
    helmChart:
      template: quay.io/coreos/stateless-app-operator@sha256:asdf123
      values:
        replicas: 2
  resources:
  - name: coreos-website.web.coreos.com
    spec:
      group: web.coreos.com
      version: v1
      scope: Namespaced
      names:
        plural: coreos-websites
        singular: coreos-website
        kind: CoreOSWebsite
        shortNames:
        - site
    outputs:
    - name: endpoint
      type: url
      description: The URL at which the website is eventually deployed
```

```sh
kubectl create -f wwwcoreoscom.yaml
```

```yaml
apiVersion: web.coreos.com/v1
kind: CoreOSWebsite
metadata:
  name: www-coreos-com
  namespace: default
spec:
  type: helm
  helmChart:
    template: quay.io/coreos/web:stable
    values:
      endpoint: www.coreos.com
    upgrades:
      automatic: True
      strategy: rolling
```


## Updating an AppType

Recall the Etcd AppType from above:

```yaml
apiVersion: app.coreos.com/v1beta1
kind: AppType
metadata:
  # (name, version, type) is unique and immutable
  name: etcd
  version: 1  
  type: com.tectonic.storage
spec:
  operator:
    # an install strategy for the operator.
    type: helm
    helmChart:
      template: quay.io/coreos/etcd-operator@sha256:dc2jk
      values:
        replicas: 2
  resources:
  # A resource is a CRD that the operator watches, along with definitions of outputs
  - name: etcds.v1alpha1.etcd.coreos.com
    spec:
      group: v1alpha1.etcd.coreos.com
      version: v1alpha1
      scope: Namespaced
      names:
        plural: etcds
        singular: etcd
        kind: EtcdCluster
      validation:
        openAPIv3: |
          {
            // real json schema
            "size": "int",
            "version": "string",
            "autoMinorVersionUpgrade": "bool"
          } 
    outputs:
    # These should be added as status fields to EtcdCluster CR by the etcd operator
    - name: service-name
      type: string
      description: The service name at which to contact the newly formed etcd
```

In this case, `EtcdCluster` is the CRD that represents a cluster in the `etcd-operator`.


We write a new version of the operator which supports writing back certs as output. To use it, we publish a new AppType:

```yaml
# ...
metadata:
  # ...
  version: 2
spec:
  operator:
    # ...
    helmChart:
      template: quay.io/coreos/etcd-operator@sha256:abf45
  resources:
  - name: etcds.v1alpha1.etcd.coreos.com
    # ... 
    outputs:
    - name: service-name
      type: string
      description: The service name at which to contact the newly formed etcd
    - name: client-cert-secret
      type: string
      description: The k8s secret at which to find the credentials to auth with the cluster
    - name: client-cert-key-secret
      type: string
      description: The k8s secret at which to find the private key for authenticating with the cluster
```

Notice that the helm chart sha changed, the app type version changed, and the outputs changed.

Later, we make another change to the operator to support a new schema for the EtcdCluster CRs, 
which revs the EtcdCluster CRD version. 

```yaml
# ...
metadata:
  # ...
  version: 3  
spec:
  operator:
    # ...
    helmChart:
      template: quay.io/coreos/etcd-operator@sha256:ol00j
  resources:
    - name: etcds.v1alpha1.etcd.coreos.com
      # ... 
    - name: etcds.v1alpha2.etcd.coreos.com
      spec:
        group: v1alpha2.etcd.coreos.com
        version: v1alpha2
        scope: Namespaced
        names:
          plural: etcds
          singular: etcd
          kind: EtcdCluster
      validation:
        openAPIv3: |
          {
            // real json schema
            "size": "int",
            "version": "string",
            "autoMinorVersionUpgrade": "bool",
            "new": "field"
          }   
      outputs:
      - name: service-name
        type: string
        description: The service name at which to contact the newly formed etcd
      - name: client-cert-secret
        type: string
        description: The k8s secret at which to find the credentials to auth with the cluster
      - name: client-cert-key-secret
        type: string
        description: The k8s secret at which to find the private key for authenticating with the cluster
```

This new operator is backwards-compatible with v1alpha1, so both v1alpha1 and v1alpha2 are listed as resources.


### ALM Operator Reconciliation Loops

1. Watches for new AppType definitions and installs defined operators and CRDs.
1. Watches CRDs for new definitions that have `ownerReference` set to ALM.
    1. Queries catalog for the highest version AppType that lists the CRD as an instance.
    1. Installs AppType, if found.
    1. If no AppType exists in the cluster (installed manually or discovered), status is written back to the CRD about the failure.
1. Watches CustomResources (instances of CRDs) that it has an AppType installed for.
    1. If operator is not yet installed, installs operator according to the install strategy for the AppType (operator field)
1. Tracks catalog for new AppType versions higher than those installed.
    1. If all resources managed by the current AppType are also managed by the new AppType, the new AppType can be installed.
        1. If auto-update is enabled, the AppType will be installed in the cluster and the new operator/CRDs will be installed. 
        1. If manual update is enabled, the AppType will be available for installation in the UI.
    1. If there are resources managed by the current AppType that are not managed by the new AppType, the new AppType is not available for installation.
        1. If auto-update is enabled, this results in a cluster alert.
        1. If manual updated is anbled, the AppType will be visible in the UI but not available for installation. 
        1. In all cases, steps can be communicated to the user on how to enable the update to proceed.
        1. Note that this will only be a problem when the `resource` definitions deprecate a version of a CRD, which should correspond to major version changes in the operator.