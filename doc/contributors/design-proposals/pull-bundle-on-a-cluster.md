# Pull Bundle on a Cluster

## Store a bundle in a `ConfigMap`
Below is the directory layout of the operator bundle inside the image.
```bash
$ tree
/
├── manifests
│   ├── testbackup.crd.yaml
│   ├── testcluster.crd.yaml
│   ├── testoperator.v0.1.0.clusterserviceversion.yaml
│   └── testrestore.crd.yaml
└── metadata
    └── annotations.yaml
    
$ cat /annotations.yaml
annotations:
  operators.coreos.com.bundle.resources: "manifests+metadata"
  operators.coreos.com.bundle.mediatype: "operator-registry+v1"
```

The following `ConfigMap` maps to the above operator bundle
```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: test
  namespace: test
  annotations:
    operators.coreos.com.bundle.resources: "manifests+metadata"
    operators.coreos.com.bundle.mediatype: "registry+v1"
data:
  testbackup.crd.yaml: content of testbackup.crd.yaml
  testcluster.crd.yaml: content of testcluster.crd.yaml
  testoperator.v0.1.0.clusterserviceversion.yaml: content oftestoperator.v0.1.0.clusterserviceversion.yaml
  testrestore.crd.yaml: content of testrestore.crd.yaml
```

The `key` of a `ConfigMap` has the following format
```go
	// Data contains the configuration data.
	// Each key must consist of alphanumeric characters, '-', '_' or '.'.
	// Values with non-UTF-8 byte sequences must use the BinaryData field.
	// The keys stored in Data must not overlap with the keys in
	// the BinaryData field, this is enforced during validation process.
	// +optional
	Data map[string]string `json:"data,omitempty" protobuf:"bytes,2,rep,name=data"`
```

Notes:
* The resource file name needs to be manipulated if it contains special characters.
* The consumer of the `ConfigMap` does will not use the key name in `Data` section to identify the type of resource. It should inspect the content.
* The consumer will iterate through the `Data` section and and add each resource to the bundle.
* The annotations from the `annotations.yaml` file is copied to `metadata.annotations` to the `ConfigMap`
* The `ConfigMap` may have a resource that contains a `PackageManifest` resource. The consumer needs to handle this properly.


## Build a Bundle from ConfigMap
```go
import (
	"github.com/operator-framework/operator-registry/pkg/registry"
	corev1 "k8s.io/api/core/v1"
)

// Manifest contains a bundle and a PackageManifest.
type Manifest struct {
	Bundle          *registry.Bundle
	PackageManifest *registry.PackageManifest
}

type Loader interface {
	Load(cm *corev1.ConfigMap) (manifest *Manifest, err error)
}
```

## Managing Lifecycle of the `ConfigMap`

## Things that Happen after reading the configmap


