# Operator Bundle

An `Operator Bundle` is a container image that stores Kubernetes manifests and metadata associated with an operator. A bundle is meant to present a specific version of an operator.

## Operator Bundle Overview

The operator manifests refers to a set of Kubernetes manifest(s) the defines the deployment and RBAC model of the operator. The operator metadata on the other hand are, but not limited to:
* Information that identifies the operator, its name, version etc.
* Additional information that drives the UI:
    * Icon
    * Example CR(s)
* Channel(s)
* API(s) provided and required.
* Related images.

An `Operator Bundle` is built as scratch (non-runnable) container image that contains operator manifests and specific metadata in designated directories inside the image. Then, it can be pushed and pulled from an OCI-compliant container registry. Ultimately, an operator bundle will be used by [Operator Registry](https://github.com/operator-framework/operator-registry) and [Operator-Lifecycle-Manager (OLM)](https://github.com/operator-framework/operator-lifecycle-manager) to install an operator in OLM-enabled clusters.

### Bundle Annotations

We use the following labels to annotate the operator bundle image.
* The label `operators.operatorframework.io.bundle.resources` represents the bundle type:
    * The value `manifests` implies that this bundle contains operator manifests only.
    * The value `metadata` implies that this bundle has operator metadata only.
    * The value `manifests+metadata` implies that this bundle contains both operator metadata and manifests.
* The label `operators.operatorframework.io.bundle.mediatype` reflects the media type or format of the operator bundle. It could be helm charts, plain Kubernetes manifests etc.

The labels will also be put inside a YAML file, as shown below.

*annotations.yaml*
```yaml
annotations:
  operators.operatorframework.io.bundle.resources: "manifests+metadata"
  operators.operatorframework.io.bundle.mediatype: "registry+v1"
```

*Notes:*
* In case of a mismatch, the `annotations.yaml` file is authoritative because on-cluster operator-registry that relies on these annotations has access to the yaml file only.
* The potential use case for the `LABELS` is - an external off-cluster tool can inspect the image to check the type of a given bundle image without downloading the content.

This example uses [Operator Registry Manifests](https://github.com/operator-framework/operator-registry#manifest-format) format to build an operator bundle image. The source directory of an operator registry bundle has the following layout.
```
$ tree test
test
├── 0.1.0
│   ├── testbackup.crd.yaml
│   ├── testcluster.crd.yaml
│   ├── testoperator.v0.1.0.clusterserviceversion.yaml
│   └── testrestore.crd.yaml
└── annotations.yaml
```

### Bundle Dockerfile

This is an example of a `Dockerfile` for operator bundle:
```
FROM scratch

# We are pushing an operator-registry bundle
# that has both metadata and manifests.
LABEL operators.operatorframework.io.bundle.resources=manifests+metadata
LABEL operators.operatorframework.io.bundle.mediatype=registry+v1

ADD test/0.1.0 /manifests
ADD test/annotations.yaml /metadata/annotations.yaml
```

Below is the directory layout of the operator bundle inside the image:
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
```

## Operator Bundle Command-Line Tool

A CLI tool is available to generate Bundle annotations and Dockerfile based on provided operator manifests.

### Bundle CLI

In order to build `operator-cli` CLI tool, follow these steps:

1. Clone [Operator-Lifecycle-Manager (OLM)](https://github.com/operator-framework/operator-lifecycle-manager) repository.
2. Build `operator-cli` binary:
```bash
$ go build ./cmd/operator-cli/
```

Now, a binary named `operator-cli` is available in OLM's directory to use.
```bash
$ ./operator-cli
Generate operator bundle metadata and build bundle image.

Usage:
   bundle [command]

Available Commands:
  build       Build operator bundle image
  generate    Generate operator bundle metadata and Dockerfile

Flags:
  -h, --help   help for bundle

Use " bundle [command] --help" for more information about a command.
```

### Generate Bundle Annotations and DockerFile

Using `operator-cli` CLI, bundle annotations can be generated from provided operator manifests. The command for `generate` task is:
```bash
$ ./operator-cli bundle generate --directory /test/0.0.1/
```
The `--directory` or `-d` specifies the directory where the operator manifests are located. The `annotations.yaml` and `Dockerfile` are generated in the same directory where the manifests folder is located (not where the YAML manifests are located). For example:
```bash
$ tree test
test
├── 0.0.1
│   ├── testbackup.crd.yaml
│   ├── testcluster.crd.yaml
│   ├── testoperator.v0.1.0.clusterserviceversion.yaml
│   └── testrestore.crd.yaml
├── annotations.yaml
└── Dockerfile
```

Note: If there are `annotations.yaml` and `Dockerfile` existing in the directory, they will be overwritten.

### Build Bundle Image

Operator bundle image can be built from provided operator manifests using `build` command:
```bash
$ ./operator-cli bundle build --directory /test/0.0.1/ --tag quay.io/coreos/test-operator.v0.0.1:latest
```
The `--directory` or `-d` specifies the directory where the operator manifests are located. The `--tag` or `-t` specifies the image tag that you want the operator bundle image to have. By using `build` command, the `annotations.yaml` and `Dockerfile` are automatically generated in the background.

The default image builder is `Docker`. However, ` Buildah` and `Podman` are also supported. An image builder can specified via `--image-builder` or `-b` optional tag in `build` command. For example:
```bash
$ ./operator-cli bundle build --directory /test/0.0.1/ --tag quay.io/coreos/test-operator.v0.0.1:latest --image-builder podman
```
