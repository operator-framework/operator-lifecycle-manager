# Installing ALM

ALM deployment resources are templated so that they can be easily configured for different deployment environments.

## Run locally with minikube 

This command starts minikube, builds the ALM containers locally with the minikube-provided docker, and uses the local configuration in [local-values.yaml](local-values.yaml) to build localized deployment resources for ALM.
```
make run-local
``` 

You can verify that the ALM components have been successfully deployed by running `kubectl -n local get deployments`

## Run locally with minishift

This command starts minishift, builds the ALM containers locally with the minishift-provided docker, and uses the local configuration in [local-values-shift.yaml](local-values-shift.yaml) to build localized deployment resources for ALM.
```
make run-local-shift
```

You can verify that the ALM components have been successfully deployed by running `kubectl -n local get deployments`

## Building deployment resources for any cluster 

Deployments of ALM can be stamped out with different configurations by writing a `values.yaml` file and running commands to generate resources.

Here's an example `values.yaml`

```yaml
# sets the apiversion to use for rbac-resources. Change to `authorization.openshift.io` for openshift
rbacApiVersion: rbac.authorization.k8s.io
# namespace is the namespace the operators will _run_
namespace: local
# watchedNamespaces is a comma-separated list of namespaces the operators will _watch_ for ALM resources. 
# Omit to enable ALM in all namespaces
watchedNamespaces: local
# catalog_namespace is the namespace where the catalog operator will look for global catalogs.
# entries in global catalogs can be resolved in any watched namespace
catalog_namespace: local

# alm operator run configuration
alm:
  # ALM operator doesn't do any leader election (yet), set to 1
  replicaCount: 1
  # The image to run. If not building a local image, use sha256 image references
  image:
    ref: quay.io/coreos/alm:local
    pullPolicy: IfNotPresent
  service:
    # port for readiness/liveness probes
    internalPort: 8080

# catalog operator run configuration
catalog:
  # Catalog operator doesn't do any leader election (yet), set to 1
  replicaCount: 1
  # The image to run. If not building a local image, use sha256 image references
  image:
    ref: quay.io/coreos/catalog:local
    pullPolicy: IfNotPresent
  service:
    # port for readiness/liveness probes
    internalPort: 8080
```

To configure a release of ALM for installation in a cluster:

1. Create a `my-values.yaml` like the example above with the desired configuration or choose an existing one from this repository. The latest production values can be found in [deploy/tectonic-alm-operator/values.yaml](../../deploy/tectonic-alm-operator/values.yaml).
1. Generate deployment files from the templates and the `my-values.yaml` using `package-release.sh`
   ```bash
   # first arg must be a semver-compatible version string
   # second arg is the output directory
   # third arg is the values.yaml file
   ./scripts/package-release.sh 0.4.0-myalm ./my-alm-deployment my-values.yaml
   ```
1. Deploy to kubernetes: `kubectl apply -f ./my-alm-deployment`

Additional steps if using official (private images):

1. Create [coreos-pull-secret](coreos-pull-secret.yml) in the namespace ALM/Catalog will run (`namespace` field in `values.yaml`)

The above steps are automated for official releases with `make ver=0.3.0 release`, which will output new versions of manifests in `deploy/tectonic-alm-operator/manifests/$(ver)`.


## Subscribe to a Package and Channel

Cloud Services can be installed from the catalog by subscribing to a channel in the corresponding package.

If using one of the `local` run options, this will subscribe to `etcd`, `vault`, and `prometheus` operators. Subscribing to a service that doesn't exist yet will install the operator and related CRDs in the namespace.

```yaml
apiVersion: app.coreos.com/v1alpha1
kind: Subscription-v1
metadata:
  name: etcd
  namespace: local 
spec:
  channel: alpha
  name: etcd
  source: tectonic-ocs
---
apiVersion: app.coreos.com/v1alpha1
kind: Subscription-v1
metadata:
  name: vault
  namespace: local
spec:
  channel: alpha
  name: vault
  source: tectonic-ocs
---
apiVersion: app.coreos.com/v1alpha1
kind: Subscription-v1
metadata:
  name: prometheus
  namespace: local
spec:
  channel: alpha
  name: prometheus
  source: tectonic-ocs
```
