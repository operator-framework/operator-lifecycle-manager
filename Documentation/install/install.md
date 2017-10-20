# Install Guide

## Prereqs

 - Kubernetes 1.8 Cluster
   - 1.7 will work, but CRs will not be validated against the schema in the corresponding CRD
 - Kubectl configured to talk to it

### Create Namespace

If not on tectonic already, create the tectonic-system namespace.

```sh
kubectl create ns tectonic-system
```

## Install 

* Create a pull secret `coreos-pull-secret` that can read:
  * `quay.io/coreos/alm`
  * `quay.io/coreos/catalog`
  * `quay.io/coreos/vault-operator`
  * `quay.io/coreos/vault`
  * `quay.io/coreos/prometheus-operator`
  * `quay.io/coreos/etcd-operator`

```bash
kubectl apply .
```

## Install a service

Cloud Services can be installed from the catalog in the tectonic UI.

If not using tectonic, they can be installed by creating an `InstallPlan-v1` resource in the desired namespace.

For example:

```bash
apiVersion: app.coreos.com/v1alpha1
kind: InstallPlan-v1
metadata:
  namespace: default
  name: etcd-installplan
spec:
  clusterServiceVersionNames:
  - etcdoperator.v0.6.0
  approval: Automatic
```

Current valid clusterServiceVersionNames:

 * etcdoperator.v0.6.0
 * prometheusoperator.0.14.0
 * vault-operator.0.1.2
