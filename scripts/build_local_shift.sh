#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

MINISHIFT_ENABLE_EXPERIMENTAL=y minishift start --service-catalog --openshift-version=v3.7.1 || { echo 'Cannot start shift.'; exit 1; }
eval $(minishift docker-env) || { echo 'Cannot switch to minishift docker'; exit 1; }
eval $(minishift oc-env) || { echo 'Cannot configure oc env'; exit 1; }
oc login -u system:admin
docker build -t quay.io/coreos/catalog:local -t quay.io/coreos/alm:local -f e2e-local-shift.Dockerfile .
