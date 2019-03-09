#!/usr/bin/env bash

# Note: run from root dir

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

# wait for deployments to be ready
kubectl rollout status -w deployment/olm-operator --namespace="${namespace}"
kubectl rollout status -w deployment/catalog-operator --namespace="${namespace}"

retries=50
until [[ $retries == 0 || $new_csv_phase == "Succeeded" ]]; do
    new_csv_phase=$(kubectl get csv -n "${namespace}" packageserver.v1.0.0 -o jsonpath='{.status.phase}' 2>/dev/null || echo "Waiting for CSV to appear")
    if [[ $new_csv_phase != "$csv_phase" ]]; then
        csv_phase=$new_csv_phase
        echo "Package server phase: $csv_phase"
    fi
    sleep 1
    retries=$((retries - 1))
done

if [ $retries == 0 ]; then
    echo "CSV \"packageserver.v1.0.0\" failed to reach phase succeeded"
    exit 1
fi

kubectl rollout status -w deployment/packageserver --namespace="${namespace}"
