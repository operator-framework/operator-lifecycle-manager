## Tooling

### Requirements

| Requirement | Purpose               | macOS                |
|-------------|-----------------------|----------------------|
| Go          | Compiler              | brew install go      |
| Docker      | Packaging             | [Docker for Mac]     |
| kubebuilder | Testing               | [kubebuilder docs]   |

[Docker for Mac]: https://store.docker.com/editions/community/docker-ce-desktop-mac
[kubebuilder docs]: https://book.kubebuilder.io/quick-start.html#installation

#### E2E test environments

| Requirement | install docs         |
|-------------|----------------------|
| Kind        | [Kind docs]          |

[Kind docs]: https://kind.sigs.k8s.io/docs/user/quick-start

### Usage

#### Testing

This project uses the built-in testing support for golang.

Envtest is also used and needs to be set up. Follow [controller-runtime instructions] and set `KUBEBUILDER_ASSETS` environment variable to point to the installation directory, for instance: `/usr/local/kubebuilder/bin`.

To run the tests for all go packages outside of the vendor directory, run:
```sh
$ make test
```

To run the e2e tests locally:

```sh
$ make e2e-local
```

**NOTE:** If you want to run the e2e tests, you need to make sure Kind is deployed in the local environment and switch the kubeconfig to an existing Kind cluster.

To run a specific e2e test locally:

```sh
$ make e2e-local TEST=TestCreateInstallPlanManualApproval
```

##### Updating test images

Sometimes you will need to update the index or bundle images used in the unit or e2e tests. To update those images, you will need to be logged into quay.io with membership in the `olmtest` organization. Then simply run `./scripts/build_e2e_test_images.sh` which will build all the required bundle images, push those to quay.io, build all the index images, and push those to quay.io as well.

Please be aware that these scripts push directly to the image tags used by the actual e2e tests.

The contents and Containerfiles of these bundles can be found in `./test/images/`.

[controller-runtime instructions]: https://pkg.go.dev/sigs.k8s.io/controller-runtime/tools/setup-envtest#section-readme

#### Building

Ensure your version of go is up to date; check that you're running the same version as in go.mod with the
commands:
```sh
$ head go.mod
$ go version
```

To build the go binary, run:
```sh
$ make build
```

#### Packaging

ALM is packaged as a set of manifests for a tectonic-x-operator specialization (tectonic-alm-operator).

A new version can be generated from the helm chart by:

 1. Modifying the `deploy/tectonic-alm-operator/values.yaml` file for the release to include new SHAs of the container images. 
 1. Running the `package` make command, which takes a single variable (`ver`)
 
For example:

```
make ver=0.3.0 package
``` 

Will generate a new set of manifests from the helm chart in `deploy/chart` combined with the `values.yaml` file in `deploy/tectonic-alm-operator`, and output the rendered templates to `deploy/tectonic-alm-operator/manifests/0.3.0`.

See the documentation in `deploy/tectonic-alm-operator` for how to take the new manifests and package them as a new version of `tectonic-alm-operator`.
 
### Dependency Management

#### Using make
These commands are handled for you via the Makefile. To install the project
dependencies, run:

```sh
$ make vendor
```

To update dependencies, run:

```sh
$ make vendor-update
# verify changes
$ make test
$ make e2e-local-docker
```

The Makefile recipes for testing and builds ensure the project's dependencies
are properly installed and vendored before running.
