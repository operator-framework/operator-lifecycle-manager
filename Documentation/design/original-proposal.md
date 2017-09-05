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
kind: App
metadata:
  name: etcd
  type: com.tectonic.storage
spec:
  operator:
    type: helm
    helmChart:
      # This contains the deployment for the controller
      # this will watch the channel on Quay and upgrade the operator when necessary
      template: quay.io/coreos/etcd-operator:stable
      values:
        replicas: 2
  # This writes out the CRDs for the app type and allows for linking app type instances to the app
  resources:
  - name: cluster.etcd.coreos.com
    spec:
      # group name to use for REST API: /apis/<group>/<version>
      group: etcd.coreos.com
      # version name to use for REST API: /apis/<group>/<version>
      version: v1
      # either Namespaced or Cluster
      scope: Namespaced
      names:
        # plural name to be used in the URL: /apis/<group>/<version>/<plural>
        plural: etcds
        # singular name to be used as an alias on the CLI and for display
        singular: etcd
        # kind is normally the CamelCased singular type. Your resource manifests use this.
        kind: Etcd
    schema: "Putting something here would be super useful for showing how to interact with the operator"
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

### List the app types

```sh
kubectl get Apps
NAME                 KIND
etcd                 Etcd.v1.etcd.coreos.com
```

### Create an instance of the app

```sh
kubectl create -f myetcd.yaml
```

```yaml
apiVersion: etcd.coreos.com/v1
kind: Etcd
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
kubectl get Etcds
NAME                 KIND
etcd-purple-ant      Etcd.v1.etcd.coreos.com
```

### Get details about the app

```sh
kubectl get etcd etcd-purple-ant -o yaml
```

```yaml
apiVersion: etcd.coreos.com/v1
kind: Etcd
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
kind: App
metadata:
  name: coreos-website
  type: com.tectonic.web
spec:
  operator:
    type: helm
    helmChart:
      template: quay.io/coreos/stateless-app-operator:stable
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
