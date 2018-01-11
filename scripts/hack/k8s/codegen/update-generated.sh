#!/usr/bin/env bash

# TODO: Have a verify-generated.sh to check in CI

set -o errexit
set -o nounset
set -o pipefail

DOCKER_REPO_ROOT="/go/src/github.com/coreos-inc/alm"
IMAGE=${IMAGE:-"gcr.io/coreos-k8s-scale-testing/codegen"}

docker run --rm -ti \
  -v "$PWD":"$DOCKER_REPO_ROOT" \
  -w "$DOCKER_REPO_ROOT" \
  "$IMAGE" \
  "./scripts/hack/k8s/codegen/run_in_docker.sh"
