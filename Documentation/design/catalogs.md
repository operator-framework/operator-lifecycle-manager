# CatalogSources

A CatalogSource indicates to OLM where to find the definitions of operators to install into a cluster when resolving
a set of Subscriptions.

# Namespacing

OLM has one namespace configured to be the "global" namespace. Any CatalogSources in the global namespace are available for installation in all namespaces.
Otherwise, packages can only be installed from CatalogSources in the same namespace as the Subscription.

# Grpc Catalogs

The primary way catalogs are provided to olm is via the grpc type. An example:

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: operatorhubio-catalog
  namespace: olm
spec:
  sourceType: grpc
  image: quay.io/operator-framework/upstream-community-operators:latest
  displayName: Community Operators
  publisher: OperatorHub.io
```

When OLM sees this type of Catalog, it pulls the image specified by the `image` field, and configures a Pod and Service
in the cluster for OLM to talk to. 

Grpc catalogs use the API defined by [operator-registry](https://github.com/operator-framework/operator-registry).


## PodSelector

There are some cases where you may not wish for OLM to control the creation of the pod that is providing the registry
api. In that case, you can create a CatalogSource that selects a pod instead. OLM will create only the Service object
needed to talk to the registry pod indicated by the selector.


```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: operatorhubio-catalog
  namespace: olm
spec:
  sourceType: grpc
  podSelector:
    matchLabels:
      olm.catalog: operatorhub-gh67sf
  displayName: Community Operators
  publisher: OperatorHub.io
```

## Address

There may be cases where having OLM manage the service for a Catalog is undesirable. In those cases, the `address` field
can be used to indicate to OLM where the grpc api should be available.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: operatorhubio-catalog
  namespace: olm
spec:
  sourceType: grpc
  address: 70.120.50.44:50051 
  podSelector:
    matchLabels:
      olm.catalog: operatorhub-gh67sf
  displayName: Community Operators
  publisher: OperatorHub.io
```

Note that `podSelector` is still required - this is so that OLM can enforce access control over the catalog data.

# ConfigMaps


Catalog data can be provided to OLM via confimaps as well. This is not recommended, as configmap catalogs do not support
all of the features that the grpc catalogs do.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: olm-operators
  namespace: olm
spec:
  sourceType: configmap
  configMap: olm-operators
  displayName: OLM Operators
  publisher: Red Hat
```

This indicates that catalog data can be found in a configmap in the same namespace called `olm-operators`. The configmap
is expected to have a specific format:

```yaml
data:
  customResourceDefinitions: |-
    - <crd definition>
    - <crd definition>
  clusterServiceVersions: |-
    - <csv yaml>
    - <csv yaml> 
  packages: |-
    - packageName: packageserver
      channels:
      - name: alpha
        currentCSV: packageserver.v0.9.0
```

The contents of each key should be a string containing a yaml list of of yaml data.
