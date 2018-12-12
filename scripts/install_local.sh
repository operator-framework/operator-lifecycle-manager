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

# wait for packageserver deployment to be ready
retries=10
until [[ $retries == 0 ||  $(kubectl rollout status -w deployment/packageserver --namespace=${namespace}) ]]; do
    sleep 5
    retries=$((retries - 1))
    echo "retrying check rollout status for deployment \"packageserver\"..."
done

if [ $retries == 0 ]
then
    echo "deployment \"packageserver\" failed to roll out"
    exit 1
fi

 echo "deployment \"packageserver\" successfully rolled out"
