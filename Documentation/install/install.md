# Install Guide

## Minikube

If you want to use minikube to run ALM, use the provided script:

```sh
./minikube.sh
```

## Existing Cluster 

If not using minikube, you will need a Tectonic cluster >= 1.7

* Create a pull secret `coreos-pull-secret` that can read:
  * `quay.io/coreos/alm`
  * `quay.io/coreos/catalog`
  * `quay.io/coreos/vault-operator`
  * `quay.io/coreos/vault`
  * `quay.io/coreos/prometheus-operator`
  * `quay.io/coreos/etcd-operator`

```bash
kubectl apply ./alm_resources
```

## Install a service manually

Cloud Services can be installed from the catalog in the tectonic UI.

If not using tectonic console, they can be installed by creating an `InstallPlan-v1` resource in the desired namespace.

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
 
## View OCS Dashboards

The latest versions of console will show an Open Cloud Service dashboard if ALM is installed.

1. Get the Console UI if you don't have it: github.com/coreos-inc/bridge
2. You can set the tectonic license for your local instance by setting `BRIDGE_LICENSE_FILE` before running.
3. Follow the [instructions](https://github.com/coreos-inc/bridge#configure-the-application) to run console pointing to the cluster with ALM running.

