#!/usr/bin/env bash

# Note: run from root dir

set -e

if [[ ${#@} < 2 ]]; then
    echo "Usage: $0 namespace chart"
    echo "* namespace: namespace to install into"
    echo "* chart: directory of chart manifests to install"
    exit 1
fi

namespace=$1
chart=$2

# create OLM
for f in ${chart}/*.yaml
do
    if [[ $f == *.configmap.yaml ]]
    then
        kubectl replace --force -f ${f};
    else
        kubectl apply -f ${f};
    fi
done

# wait for deployments to be ready
kubectl rollout status -w deployment/olm-operator --namespace=${namespace}
kubectl rollout status -w deployment/catalog-operator --namespace=${namespace}
kubectl rollout status -w deployment/package-server --namespace=${namespace}
