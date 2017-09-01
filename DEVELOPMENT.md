## Tooling

### Requirements

| Requirement | Purpose               | macOS            |
|-------------|-----------------------|------------------|
| Go          | Compiler              | brew install go  |
| Dep         | Dependency Management | brew install dep |
| Docker      | Packaging             | [Docker for Mac] |

[Docker for Mac]: https://store.docker.com/editions/community/docker-ce-desktop-mac

### Usage

#### Dependency Management

This project uses [dep] for managing dependencies.

[dep]: https://github.com/golang/dep

To install the projects dependencies into the `vendor` directory, run the following command:

```sh
$ dep ensure
```

To add a new dependency, run the following command:

```sh
$ dep ensure -add github.com/foo/bar
```

To update a particular dependency, run the following command:

```sh
$ dep ensure github.com/foo/bar@^1.0.1
```

To update all existing dependencies, run the following command:

```sh
$ dep ensure -update
```
