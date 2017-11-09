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
kubectl apply -f ./alm_resources
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
  - etcdoperator.v0.6.1
  approval: Automatic
```

Current valid clusterServiceVersionNames:

 * etcdoperator.v0.6.1
 * prometheusoperator.0.14.0
 * vault-operator.0.1.3
 
## View OCS Dashboards

### Updating an existing console

If you are running an existing Tectonic Cluster, the Console will need to be updated to the `service-catalog-alpha` tag
to display the OCS.

1. Navigate to the `tectonic-console` Deployment
2. Click on the `YAML` tab
3. Under the `image` section, change the image to `quay.io/coreos/tectonic-console:service-catalog-alpha`
4. Save and wait for Console to reboot
5. Hard refresh Console to see the OCS

### Installing a new console

The `service-catalog-alpha` tagged version of console will show an Open Cloud Service dashboard if ALM is installed. Follow the [instructions](https://github.com/coreos-inc/bridge#configure-the-application) to run console pointing to the cluster with ALM running, making sure to use
a deployment with the `quay.io/coreos/tectonic-console:service-catalog-alpha` image.

