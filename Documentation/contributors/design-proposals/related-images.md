# Related Images

Status: Pending

Version: Alpha

Implementation Owner: ecordell 

## Motivation

Operators often need to make use of other container images to perform their functions as operators. 

## Proposal

We will take advantage of the [images metadata](https://github.com/kubernetes-sigs/kustomize/blob/master/docs/fields.md#images)
for kustomization files and include a `kustomization.yaml` file within the bundle. 

This can be used to write down additional metadata, or as a way to 
change only the images in a bundle without touching the rest of the definition (i.e. as part of a CI/CD process).

```yaml
images:
- name: postgres
  newName: my-registry/my-postgres
  newTag: v1
- name: nginx
  newTag: 1.8.0
- name: my-demo-app
  newName: my-app
- name: alpine
  digest: sha256:24a0c4b4a4c0eb97a1aabb8e29f18e917d05abfe1b7a7c07857230879ce7d3d3
```

([image type](https://github.com/kubernetes-sigs/kustomize/blob/master/pkg/image/image.go))

The images in the `image` list will be used to determine the set of related images that are required for the operator.

The images in this list will be considered related even if applying the config to the ClusterServiceVersion would not
transform it (i.e. the `name` of an image does not need to exist in the CSV).

The kustomization will be applied to the CSV, so the `image` config may be used to overwrite the images in the deployment in the CSV.

### Why kustomize?

There are several other existing approaches to associating images with an application.

CNAB: Using CNAB's `bundle.json` would allow us to associate image metadata, but comes with a heavy spec. CNAB bundles cannot be applied directly to a kubernetes cluster, they require additional tooling.

ImageStream: OpenShift ClusterOperators include an `image-references` file that contains an ImageStream object. This allows listing objects, but is not meaningful when applied to a cluster (despite being a real object), and can only be applied to OpenShift clusters.

By using Kustomize's metadata, we:

- Have a way to list out images needed by the operator
- Have a way to override the images needed by the operator (without touching the base manifests)
- Retain `kubectl` compatibility; operator bundles can be applied to a cluster with `kubectl apply -k -f bundle`

### Operator Registry Changes

If a bundle includes an `kustomization.yaml` file, images in this file are extracted during the `load` operation of a bundle into an
operator-registry database. With the following rules:

- Images are pulled from the ClusterServiceVersion `container` definitions as if kustomize has been run over it.
- Images are pulled from the `kustomization.yaml` file regardless of whether they are "used" by the bundle.

If a bundle does not include a `kustomization.yaml` file, images are extracted from the ClusterServiceVersion `container` definitions.

The `Query` interface for an operator-registry database will have two new APIs: 

```go
type Query interface {
    // ... 
    ListImages(ctx context.Context) ([]string, error)
    GetImagesForBundle(ctx context.Context, csvName string) ([]string, error)
}
```

`ListImages` will list all images in an operator-registry database. 

### Example

```sh
$ tree bundle
bundle
├── csv.yaml
└── kustomization.yaml
```

**bundle/csv.yaml**

```yaml
  containers:
  - command:
    - etcd-operator
    - --create-crd=false
    image: quay.io/coreos/etcd-operator@sha256:66a37fd61a06a43969854ee6d3e21087a98b93838e284a6086b13917f96b0d9b
    name: etcd-operator
```

**bundle/kustomization.yaml**
```yaml
images:
- name: quay.io/coreos/etcd-operator
  newTag: latest
- name: quay.io/coreos/etcd
  newTag: 3.0.5
- name: quay.io/coreos/etcd
  digest: sha256:24a0c4b4a4c0eb97a1aabb8e29f18e917d05abfe1b7a7c07857230879ce7d3d3
resources:
  - csv.yaml
```

The list of images will then be:

```
quay.io/coreos/etcd-operator:latest
quay.io/coreos/etcd:3.0.5
quay.io/coreos/etcd@sha256:24a0c4b4a4c0eb97a1aabb8e29f18e917d05abfe1b7a7c07857230879ce7d3d3
```

Note that `quay.io/coreos/etcd-operator@sha256:66a37fd61a06a43969854ee6d3e21087a98b93838e284a6086b13917f96b0d9b` is not
included, since it would be replaced with `:latest` if the `kustomization.yaml` were applied.

### Future Work

#### Override Operand

Add a `relatedImages` field to the ClusterServiceVersion, and make use of kustomize's [transformer configs](https://github.com/kubernetes-sigs/kustomize/blob/master/examples/transformerconfigs/images/README.md) to teach it about those fields.
`relatedImages` can be projected into operator deployments via downward API, which will allow the kustomization file to override operand images in addition to opeator images.

