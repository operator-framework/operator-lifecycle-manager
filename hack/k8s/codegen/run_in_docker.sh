#!/usr/bin/env bash

# This script is designed to run inside docker.

set -o errexit
set -o nounset
set -o pipefail

source ./$(dirname "$0")/codegen.sh

api_groups=("clusterserviceversion:v1alpha1" "installplan:v1alpha1"
            "uicatalogentry:v1alpha1" "catalogsource:v1alpha1")

for group in ${api_groups[@]}; do
    echo -n "[$group] "
    codegen::generate-groups deepcopy \
        github.com/coreos-inc/alm/pkg/generated \
        github.com/coreos-inc/alm/pkg/apis \
        $group \
        --go-header-file "./hack/k8s/codegen/boilerplate.go.txt"
done
