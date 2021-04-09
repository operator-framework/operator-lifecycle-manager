#!/usr/bin/env bash


# This script is for installing OLM from a GitHub release

set -e

default_base_url=https://github.com/operator-framework/operator-lifecycle-manager/releases/download

if [[ ${#@} -lt 1 || ${#@} -gt 2 ]]; then
    echo "Usage: $0 version [base_url]"
    echo "* version: the github release version"
    echo "* base_url: the github base URL (Default: $default_base_url)"
    exit 1
fi

release="$1"
base_url="${2:-${default_base_url}}"
url="${base_url}/${release}"
namespace=olm

kubectl apply -f "${url}/crds.yaml"
kubectl wait --for=condition=Established -f "${url}/crds.yaml"
kubectl apply -f "${url}/olm.yaml"

# wait for deployments to be ready
kubectl rollout status -w deployment/olm-operator --namespace="${namespace}"
kubectl rollout status -w deployment/catalog-operator --namespace="${namespace}"

retries=30
until [[ $retries == 0 ]]; do
    new_csv_phase=$(kubectl get csv -n "${namespace}" packageserver -o jsonpath='{.status.phase}' 2>/dev/null || echo "Waiting for CSV to appear")
    if [[ $new_csv_phase != "$csv_phase" ]]; then
        csv_phase=$new_csv_phase
        echo "Package server phase: $csv_phase"
    fi
    if [[ "$new_csv_phase" == "Succeeded" ]]; then
	break
    fi
    sleep 10
    retries=$((retries - 1))
done

if [ $retries == 0 ]; then
    echo "CSV \"packageserver\" failed to reach phase succeeded"
    exit 1
fi

kubectl rollout status -w deployment/packageserver --namespace="${namespace}"
