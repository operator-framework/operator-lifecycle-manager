#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e
set -o xtrace

docker build -f e2e.Dockerfile -t quay.io/operator-framework/olm:local -t quay.io/operator-framework/olm-e2e:local ./bin
