#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CRD_PATH="${SCRIPT_ROOT}/vendor/github.com/operator-framework/api/crds"
for f in ${CRD_PATH}/*.yaml ; do
    if [[ ! "${f}" =~ .*_operators\.yaml ]]; then
        echo "copying ${f}"
        cp "${f}" "${SCRIPT_ROOT}/deploy/chart/crds/0000_50_olm_00-${f##*/operators.coreos.com_}"
    fi
done

