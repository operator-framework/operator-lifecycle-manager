# Application Lifecycle Manager

## Goals

The aim of this project is to serve the following purposes:

* CRUD operations on top level service/app types
* CRUD operations on service/app instances
* Answer forensic questions and generate audit logs about state transitions for the above
* To do so in the most kubernetes native way possible

## Usage

**Asumption**: The ALM operator is already installed in the cluster, which defines an AppType CRD and an InstallStrategy CRD

```sh
kubectl get crd
NAME                            KIND
app.stable.coreos.com           CustomResourceDefinition.v1beta1.apiextensions.k8s.io
opinstaller.stable.coreos.com   CustomResourceDefinition.v1beta1.apiextensions.k8s.io
```

### Create an instance of an app type

```sh
kubectl create -f etcdapp.yaml
```

```yaml
apiVersion: app.coreos.com/v1beta1
kind: AppType
metadata:
  # (name, type) is unique and immutable
  name: etcd
  type: com.tectonic.storage
  # generated
  selfRef: etcd.com.tectonic.storage
```

### Create an InstallStrategy

```sh
kubectl create -f etcd-op-installstrategy.yaml
```

```yaml
apiVersion: opinstaller.coreos.com/v1beta1
kind: InstallStrategy 
metadata:
  ownerReference: etcd.com.tectonic.storage
  version: 1
  selfRef: install.1.etcd.com.tectonic.storage
spec:
  # an install strategy for the operator.
  strategy: 
    type: helm
    helmChart: quay.io/coreos/etcd-operator
    sha256: aef4455
    values:
      replicas: 2
  resources:
    - etcds.1.etcd.coreos.com
```

### Create the CRDs managed by the operator

```sh
kubectl create -f etcd-crd.yaml
```

```yaml
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
metadata:
  ownerRef: etcd.com.tectonic.storage
  name: etcds.1.etcd.coreos.com
spec:
  group: 1.etcd.coreos.com
  version: 1
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
        "outputs": {
          "serviceName": "string"
        }
      } 
```

### List the app types

```sh
kubectl get Apps
NAME                 KIND
etcd                 Etcd.1.etcd.coreos.com
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
etcd-purple-ant      Etcd.1.etcd.coreos.com
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
  outputs:
    service-name: etcd-purple-ant.service
```

## Adding a new InstallStrategy and Resource

```yaml
apiVersion: opinstaller.coreos.com/v1beta1
kind: InstallStrategy 
metadata:
  version: 2
  # ...
spec:
  strategy: 
    # ...
    sha256: aef4455
  resources:
    - etcds.1.etcd.coreos.com
    - etcds.2.etcd.coreos.com
---
apiVersion: apiextensions.k8s.io/v1beta1
kind: CustomResourceDefinition
# ...
spec:
  version: 2
  # ...
  validation:
    openAPIv3: |
      {
        // real json schema
        "size": "int",
        "version": "string",
        "autoMinorVersionUpgrade": "bool",
        "outputs": {
          "serviceName": "string",
          "keySecretName": "string"
        }
      } 
```

This new operator is backwards-compatible with v1, so both v1 and v2 are listed as resources. 

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


# Future work

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
