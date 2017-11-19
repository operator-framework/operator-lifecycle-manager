## Tooling

### Requirements

| Requirement | Purpose               | macOS                |
|-------------|-----------------------|----------------------|
| Go          | Compiler              | brew install go      |
| Glide       | Dependency Management | brew install glide   |
| Docker      | Packaging             | [Docker for Mac]     |
| jsonnet     | JSON templating tool  | brew install jsonnet |
| ffctl       | Gitlab CI format      | pip install ffctl    |

[Docker for Mac]: https://store.docker.com/editions/community/docker-ce-desktop-mac

### Usage

#### Testing

This project uses the built-in testing support for golang.

To run the tests for all go packages outside of the vendor directory, run:
```sh
$ make test
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

To build the project as a Docker image, use the standard docker tooling:
```sh
$ docker build -t coreos-alm:dev ./
```

### Dependency Management

#### Using glide

This project uses [glide] for managing dependencies.

[glide]: https://github.com/Masterminds/glide

To install the projects dependencies into the `vendor` directory, run the following command:

```sh
$ glide install -v
```

To add a new dependency, run the following command:

```sh
$ glide get -v github.com/foo/bar
```

To add a new dependency at a specific version, append with `#{version}`:

```sh
$ glide get github.com/foo/bar#~1.2.0
```

To update all existing dependencies, run the following command:

```sh
$ glide up -v
```

#### Using make
These commands are handled for you via the Makefile. To install the project
dependencies, run:

```sh
$ make vendor
```

To update the dependencies, run:
```sh
$ make vendor-update
```

The Makefile recipes for testing and builds ensure the project's dependencies
are properly installed and vendored before running.
