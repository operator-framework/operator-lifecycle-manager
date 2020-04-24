# Installing OLM

OLM deployment resources are templated so that they can be easily configured for different deployment environments.

# Installation Options

## Manual installation 

Installing the CRDs first gives them a chance to register before installing the rest, which requires the CRDs exist.
```bash
kubectl create -f deploy/upstream/quickstart/crds.yaml
kubectl create -f deploy/upstream/quickstart/olm.yaml
```

## Install a Release

Check out the latest [releases on github](https://github.com/operator-framework/operator-lifecycle-manager/releases) for release-specific install instructions.

## Run locally with minikube

This command starts minikube, builds the OLM containers locally with the minikube-provided docker, and uses the local configuration in [local-values.yaml](local-values.yaml) to build localized deployment resources for OLM.

```bash
# To install and run locally
$ make run-local
```

You can verify that the OLM components have been successfully deployed by running `kubectl -n local get deployments`

**NOTE** It is recommended for development purposes and will use the source locally

## OpenShift

**IMPORTANT:** OLM is installed by default in OpenShift 4.0 and above.

## Customizing OLM installation 

Deployments of OLM can be stamped out with different configurations by writing a `values.yaml` file and running commands to generate resources.

Here's an example `values.yaml`

```yaml
# sets the apiversion to use for rbac-resources. Change to `authorization.openshift.io` for openshift
rbacApiVersion: rbac.authorization.k8s.io
# namespace is the namespace the operators will _run_
namespace: olm
# watchedNamespaces is a comma-separated list of namespaces the operators will _watch_ for OLM resources.
# Omit to enable OLM in all namespaces
watchedNamespaces: olm
# catalog_namespace is the namespace where the catalog operator will look for global catalogs.
# entries in global catalogs can be resolved in any watched namespace
catalog_namespace: olm
# operator_namespace is the namespace where the operator runs
operator_namespace: operators

# OLM operator run configuration
olm:
  # OLM operator doesn't do any leader election (yet), set to 1
  replicaCount: 1
  # The image to run. If not building a local image, use sha256 image references
  image:
    ref: quay.io/operator-framework/olm:local
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
    ref: quay.io/operator-framework/olm:local
    pullPolicy: IfNotPresent
  service:
    # port for readiness/liveness probes
    internalPort: 8080
```

To configure a release of OLM for installation in a cluster:

1. Create a `my-values.yaml` like the example above with the desired configuration or choose an existing one from this repository. The latest production values can be found in [deploy/upstream/values.yaml](../../deploy/upstream/values.yaml).
1. Generate deployment files from the templates and the `my-values.yaml` using `package_release.sh`

   ```bash
   # first arg must be a semver-compatible version string
   # second arg is the output directory
   # third arg is the values.yaml file
   ./scripts/package_release.sh 1.0.0-myolm ./my-olm-deployment my-values.yaml
   ```

1. Deploy to kubernetes: `kubectl apply -f ./my-olm-deployment/templates/`

The above steps are automated for official releases with `make ver=0.3.0 release`, which will output new versions of manifests in `deploy/tectonic-alm-operator/manifests/$(ver)`.

## Overriding the Global Catalog Namespace

It is possible to override the Global Catalog Namespace by setting the `GLOBAL_CATALOG_NAMESPACE` environment variable in the catalog operator deployment.

## Subscribe to a Package and Channel

Cloud Services can be installed from the catalog by subscribing to a channel in the corresponding package.

If using one of the `local` run options, this will subscribe to `etcd`, `vault`, and `prometheus` operators. Subscribing to a service that doesn't exist yet will install the operator and related CRDs in the namespace.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: etcd
  namespace: olm
spec:
  channel: singlenamespace-alpha
  name: etcd
  source: operatorhubio-catalog
  sourceNamespace: olm
---
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: prometheus
  namespace: olm
spec:
  channel: alpha
  name: prometheus
  source: operatorhubio-catalog
  sourceNamespace: olm
```

# Uninstall

Run the command `make uninstall`.

**NOTE** Valid just for local/manual installs. 
