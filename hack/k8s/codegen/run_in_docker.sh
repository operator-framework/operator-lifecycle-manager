#!/usr/bin/env bash

# This script is designed to run inside docker.

set -o errexit
set -o nounset
set -o pipefail

source ./$(dirname "$0")/codegen.sh

codegen::generate-groups deepcopy \
  github.com/coreos-inc/alm/generated \
  github.com/coreos-inc/alm/apis \
  opver:v1alpha1 \
  --go-header-file "./hack/k8s/codegen/boilerplate.go.txt"

codegen::generate-groups deepcopy \
  github.com/coreos-inc/alm/generated \
  github.com/coreos-inc/alm/apis \
  apptype:v1alpha1 \
  --go-header-file "./hack/k8s/codegen/boilerplate.go.txt"