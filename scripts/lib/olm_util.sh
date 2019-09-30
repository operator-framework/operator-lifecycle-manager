#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

olm::util::await_csv_success() {
    local namespace="$1"
    local csv="$2"
    local retries="${3:-50}"
    local phase

    echo "awaiting ${namespace}/${csv} csv installation success"
    until [[ "${retries}" -le "0" || "${phase:=$(kubectl get csv -n "${namespace}" "${csv}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "missing")}" == "Succeeded" ]]; do
        retries=$((retries - 1))
        echo "current phase: ${phase}, remaining attempts: ${retries}"
        unset phase
        sleep 1
    done

    if [ "${retries}" -le "0" ] ; then
        echo "${csv} csv installation unsuccessful"
        return 1
    fi

    echo "${csv} csv installation succeeded"
}

olm::util::await_olm_ready() {
    local namespace="$1"

    kubectl rollout status -w deployment/olm-operator --namespace="${namespace}" || return
    kubectl rollout status -w deployment/catalog-operator --namespace="${namespace}" || return
    olm::util::await_csv_success "${namespace}" "packageserver" 32 || return
}