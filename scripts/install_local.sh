#!/usr/bin/env bash

OLM_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
source "${OLM_ROOT}/scripts/lib/olm_util.sh"

set -e

if [[ ${#@} -ne 2 ]]; then
    echo "Usage: $0 namespace chart"
    echo "* namespace: namespace to install into"
    echo "* chart: directory of chart manifests to install"
    exit 1
fi

namespace=$1
chart=$2

# create OLM
for f in "${chart}"/*.yaml
do
    if [[ $f == *.configmap.yaml ]]
    then
        kubectl replace --force -f "${f}"
    else
        kubectl apply -f "${f}"
    fi
done

if ! olm::util::await_olm_ready "${namespace}" ; then
    echo "olm failed to become ready"
    exit 1
fi
