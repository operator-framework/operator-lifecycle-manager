# Backend end to end tests

This runs a series of tests against the Kubernetes API to verify that OLM is functioning properly.

## Requirements

* Minikube > 0.25.0
* Helm > 2.7.0

## How to use


Execute `make e2e-local` in the root of the repository, which will:

- optionally update `test/e2e/assets/chart/zz_chart.go` as needed
- build local executables used during testing
  - `bin/e2e-local.test`
  - `bin/wait`
  - `bin/cpb`
  - `bin/catalog`
  - `bin/olm`
  - `bin/package-server`
- build docker file `e2e.Dockerfile` which includes the local executables in a `kind` image archive `test/e2e-local.image.tar`
- execute `ginkgo` to run the pre-compiled test package `bin/e2e-local.test` with the `kind` image archive. This runs BDD tests defined in `test/e2e`
  - these tests are run in a kind cluster that is started fresh each time the test is executed


Examples:

- Run all BDD tests (this takes a long time)

  ```bash
  make e2e-local
  ```

- Run a specific BDD test using the `TEST` argument to make. Note that this argument uses regular expressions.

  ```bash
  make e2e-local TEST='API service resource not migrated if not adoptable'
  ```

- If you have previously created the `bin/e2e-local.test` executable and want a quick way to ensure that your TEST regex argument will work, you can bypass the 
make file and use `-dryRun` with `-focus` and see if the regex would trigger your specific test(s).
  
  ```bash
  GO111MODULE=on GOFLAGS="-mod=vendor" go run github.com/onsi/ginkgo/ginkgo -dryRun -focus 'API service resource not migrated if not adoptable' bin/e2e-local.test
  ```

- It is also possible to specify the number of parallel test nodes (i.e. one or more instances of `go test`) to run using the `NODES` argument. Defaults to 1 if not specified

  ```bash
  make e2e-local NODES=2
  ```

## Build infrastructure

Note that the make file target `e2e-local` is executed by the github workflow `.github/workflows/e2e-tests.yml` and uses two parallel `go test` processes.