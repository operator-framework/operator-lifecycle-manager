# Backend end to end tests

This runs a series of tests against the Kubernetes API to verify that OLM is functioning properly.

## Requirements

* Minikube > 0.25.0
* Helm > 2.7.0

## How to use

`make e2e-local` in the root of the repository will fetch golang dependencies, start Minikube, build the appropriate images and run the tests in a fresh namespace each time.

Subsequent runs of the test suite do not need to go through the full setup process. Running individual tests (or the whole suite) can be accomplished by running `./test/e2e/run_e2e_local.sh [TestName]` with an optional test name.
