# Related Images

Status: Pending

Version: Alpha

Implementation Owner: ecordell 

# Motivation

Operators often need to make use of other container images to perform their functions as operators. 

## Proposal

Introduce a new field `relatedImages` to the `ClusterServiceVersion` spec. 

### ClusterServiceVersion Spec Changes

A new section `relatedImages` is added to the ClusterServiceVersionSpec.

```yaml
kind: ClusterServiceVersion 
metadata:
  name: etcd-operator
spec:
  relatedImages:
  - name: default
    image: quay.io/coreos/etcd@sha256:12345 
    annotation: default
  - name: etcd-2.1.5
    image: quay.io/coreos/etcd@sha256:12345 
  - name: etcd-3.1.1
    image: quay.io/coreos/etcd@sha256:12345 
```

These will be made available as annotations on the operator deployments, so that they can be used via downward API if desired. This may be particularly useful for operators that are tightly coupled to another particular image.

```yaml
kind: Deployment
metadata:
  name: etcd-operator
  annotations:
    default: quay.io/coreos/etcd@sha256:12345 
    olm.relatedImage.etcd-2.1.5: quay.io/coreos/etcd@sha256:12345 
    olm.relatedImage.etcd-3.1.1: quay.io/coreos/etcd@sha256:12345 
spec:
  replicas: 1
  selector:
    matchLabels:
      name: etcd-operator
  template:
    metadata:
      name: etcd-operator
      labels:
        name: etcd-operator
    spec:
      serviceAccountName: etcd-operator
      containers:
      - name: etcd-operator
        command:
        - etcd-operator
        - --create-crd=false
        - --defaultImage=${DEFAULT}
        image: quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2
        env:
        - name: NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: DEFAULT
          valueFrom:
            fieldRef:
              fieldPath: metadata.annotations['default']
```

### Implementation

#### ClusterServiceVersion Spec and Status

Spec needs to be updated to include the fields described above, and the openapi validation should be updated as well.

Note that the `name` field for related images must satisfy the annotation name conventions.

#### Install Strategy

Most of the change will take place in the install strategy; which knows how to take the deployment spec defined in a CSV and check if the cluster is up-to-date, and apply changes if needed.

- The install strategy will now need to know about related images.
	- `CheckInstalled` will check that the annotations on the operator deployments include the `relatedImages` annotations.
	- `Install` will also need to project the relatedImages as annotations on the deployment.
	
#### Implementation Stages

- [ ] API Changes
- [ ] Annotation Projection on Deployments

### User Documentation

#### Associating Related Images

Operators often need to make use of other container images to perform their functions. For example, the etcd operator 
makes use of etcd container images to create etcd clusters as requested by the user.

To indicate that such images are used by the operator, a ClusterServiceVersion author can fill out the `relatedImages` 
field on the CSV spec.

These fields are optional, but should be filled out whenever possible. Tooling can take advantage of this information
to ensure that all required images are available in the cluster.

```yaml
kind: ClusterServiceVersion 
metadata:
  name: etcd-operator
spec:
  relatedImages:
  - name: default
    image: quay.io/coreos/etcd@sha256:12345 
    annotation: default
  - name: etcd-2.1.5
    image: quay.io/coreos/etcd@sha256:12345 
  - name: etcd-3.1.1
    image: quay.io/coreos/etcd@sha256:12345  
```

### Operator Registry Changes

If a CSV includes an `relatedImages` section, images in this file are extracted during the `load` operation of a bundle into an
operator-registry database. With the following rules:

- Images are pulled from the ClusterServiceVersion `container` definitions as if kustomize has been run over it.
- Images are read from the `relatedImages section`

If a bundle does not include a `relatedImages` section, images are extracted from the ClusterServiceVersion `container` definitions.

The `Query` interface for an operator-registry database will have two new APIs: 

```go
type Query interface {
    // ... 
    ListImages(ctx context.Context) ([]string, error)
    GetImagesForBundle(ctx context.Context, csvName string) ([]string, error)
}
```

`ListImages` will list all images in an operator-registry database. 


### Future Work

#### Using related images via downwardAPI

The related images can be consumed by the operator deployment. This may be useful if, for example, the operator
and operand images are tightly coupled. The `annotation` field from the `relatedImages` is used as the name of the annotation.

These will be made available as annotations on the operator deployments, so that they can be used via downward API if desired. This may be particularly useful for operators that are tightly coupled to another particular image.

```yaml
kind: Deployment
metadata:
  name: etcd-operator
  annotations:
    default: quay.io/coreos/etcd@sha256:12345 
    olm.relatedImage.etcd-2.1.5: quay.io/coreos/etcd@sha256:12345 
    olm.relatedImage.etcd-3.1.1: quay.io/coreos/etcd@sha256:12345 
spec:
  replicas: 1
  selector:
    matchLabels:
      name: etcd-operator
  template:
    metadata:
      name: etcd-operator
      labels:
        name: etcd-operator
    spec:
      serviceAccountName: etcd-operator
      containers:
      - name: etcd-operator
        command:
        - etcd-operator
        - --create-crd=false
        - --defaultImage=${DEFAULT}
        image: quay.io/coreos/etcd-operator@sha256:c0301e4686c3ed4206e370b42de5a3bd2229b9fb4906cf85f3f30650424abec2
        env:
        - name: NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        - name: DEFAULT
          valueFrom:
            fieldRef:
              fieldPath: metadata.annotations['default']
```

#### Required Images

Any of the related images may be marked required. This would prevent the operator from installing if the required image is unavailable.


```yaml
kind: ClusterServiceVersion 
metadata:
  name: etcd-operator
spec:
  relatedImages:
  - name: default
    image: quay.io/coreos/etcd@sha256:12345 
    annotation: default
    required: true
  - name: etcd-2.1.5
    image: quay.io/coreos/etcd@sha256:12345 
  - name: etcd-3.1.1
    image: quay.io/coreos/etcd@sha256:12345 
```
