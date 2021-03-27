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
| Minikube    | [Minikube docs]      |
| Kind        | [Kind docs]          |

[Minikube docs]: https://minikube.sigs.k8s.io/docs/start
[Kind docs]: https://kind.sigs.k8s.io/docs/user/quick-start

### Usage

#### Testing

This project uses the built-in testing support for golang.

To run the tests for all go packages outside of the vendor directory, run:
```sh
$ make test
```

To run the e2e tests locally:

```sh
$ make e2e-local
```

**NOTE:** Command `make e2e-local` supports Minikube and Kind environments. If you want to run the e2e tests on Minikube, you need to make sure Minikube is deployed in the local environment. If you want to run the e2e tests on Kind, you need to make sure Kind is deployed in the local environment and switch the kubeconfig to an existing Kind cluster.

To run a specific e2e test locally:

```sh
$ make e2e-local TEST=TestCreateInstallPlanManualApproval
```

#### Building

Ensure your version of go is up to date; check that you're running v1.9 with the
command:
```sh
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
