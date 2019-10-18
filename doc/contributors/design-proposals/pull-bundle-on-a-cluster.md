# Pull Bundle on a Cluster

The purpose of this proposal is to allow bundle data to be pulled at install time. At a high level, this starts with launching the bundle inside a pod and then writing the data found inside of the container into a configmap.

## Launching a bundle image for data extraction

The function to call for launching the bundle looks like:

LaunchBundleImage(kubeclient kubernetes.Interface, bundleImage, initImage, namespace string) (*corev1.ConfigMap, *batchv1.Job, error)

Here the bundle image, init image, and namespace are the core parameters. Note that it is a requirement that the service account have permissions to update configmaps in the specified namespace. In the expected usage however, the namespace of OLM will be used and permissions will not be a problem. The configmap and job are returned for the caller to delete when done.

Within the launch function a job is called using a spec simliar to what is described below. Unique random names are able to be depended upon by using generateName for both the job and configmap.

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  generateName: deploy-bundle-image-
spec:
  #ttlSecondsAfterFinished: 0 # alpha feature - https://github.com/kubernetes/enhancements/issues/592, https://github.com/kubernetes/kubernetes/pull/82082
  template:
    metadata:
      name: bundle-image
    spec:
      containers:
      - name: bundle-image
        image: &image bundle-image
        command: ['/injected/operator-registry', 'serve']
        env:
          - name: CONTAINER_IMAGE
            value: *image
        volumeMounts:
        - name: copydir
          mountPath: /injected
      initContainers:
      - name: copy-binary
        image: init-operator-manifest
        imagePullPolicy: Never
        command: ['/bin/cp', '/operator-registry', '/copy-dest']
        volumeMounts:
        - name: copydir
          mountPath: /copy-dest
      volumes:
      - name: copydir
        emptyDir: {}
      restartPolicy: OnFailure
```

## Serving the bundle data

A new command will be made in operator-registry to provide functionality for traversing the directies of the bundle image for writing to a configmap (example above is "operator-registry serve"). The configmap format is described in more detail in the next section. The pre-generated configmap name is specified to be updated with the bundle data. As previously noted, after the configmap data has been read it is the responsibility of the caller to delete both the job and configmap. As a sanity check, the configmap is also updated with the image name as an annotation (olm.imageSource).

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
* The consumer of the `ConfigMap` does not use the key name in `Data` section to identify the type of resource. It should inspect the content.
* The consumer will iterate through the `Data` section and and add each resource to the bundle.
* The annotations from the `annotations.yaml` file is copied to `metadata.annotations` to the `ConfigMap`.
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


