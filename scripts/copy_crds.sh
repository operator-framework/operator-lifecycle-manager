#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CRD_PATH="${SCRIPT_ROOT}/vendor/github.com/operator-framework/api/crds"

rm "${SCRIPT_ROOT}"/deploy/chart/crds/*.yaml
for f in "${CRD_PATH}"/*.yaml ; do
    echo "copying ${f}"
    cp "${f}" "${SCRIPT_ROOT}/deploy/chart/crds/0000_50_olm_00-$(basename "$f" | sed 's/^.*_\([^.]\+\)\.yaml/\1.crd.yaml/')"
done
