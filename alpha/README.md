# Testing the ALM Alpha in Tectonic

## Overview

ALM is a project that creates an opinionated framework for managing applications in Kubernetes.

This project enables users to do the following:

* Define applications as a single Kubernetes resource that encapsulates requirements and dashboarding metadata
* Install applications automatically with dependency resolution or manually with nothing but `kubectl`
* Upgrade applications automatically with different approval policies

This project does not:

* Replace [Helm](https://github.com/kubernetes/helm)
* Turn Kubernetes into a [PaaS](https://en.wikipedia.org/wiki/Platform_as_a_service)

For more information about the architecture of the ALM framework, read `architecture.pdf`.

## Prerequisites

* Tectonic 1.7.9
* Admin access via RBAC

## Installing ALM

The following installs the ALM operators into your cluster:

```sh
kubectl create -f ./resources
kubectl -n tectonic-system patch deployment tectonic-console --type='json' -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/image", "value":"quay.io/coreos/tectonic-console:service-catalog-alpha"}]'
```

**WARNING**: This changes the container image used by the Tectonic Console.
When updating Tectonic, you must remember to change this Deployment back to the original image tag:

```sh
kubectl -n tectonic-system patch deployment tectonic-console --type='json' -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/image", "value":"quay.io/coreos/tectonic-console:v2.3.5"}]'
```

## Enabling an Open Cloud Service via kubectl

Open Cloud Services can be installed from the catalog in the Tectonic Console UI or manually via `kubectl`.
In order to create them via `kubectl`, a user creates an `InstallPlan-v1` resource in the desired namespace.

For example:

```yaml
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

Current valid clusterServiceVersionNames include:

 * etcdoperator.v0.6.1
 * prometheusoperator.0.14.0
 * vault-operator.0.1.3

## Caveats

In order to install the Vault OCS, the namespace needs access to its private image repository.
This can be manually accomplished by copying the `coreos-pull-secret` from the `tectonic-system` namespace into the desired namespace.

Replacing `YOURNAMESPACE` in the following command with the name of the desired namespace will copy said pull secret:

```sh
kubectl get secrets -n tectonic-system -o yaml coreos-pull-secret | sed 's/tectonic-system/YOURNAMESPACE/g' | kubectl create -f -
```
