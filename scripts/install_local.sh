#!/usr/bin/env bash

# Note: run from root

set -e

if [[ ${#@} < 2 ]]; then
    echo "Usage: $0 namespace chart"
    echo "* namespace: namespace to install into"
    echo "* chart: directory of chart manifests to install"
    exit 1
fi

namespace=$1
chart=$2

# create alm NS
kubectl create ns ${namespace} || { echo 'ns exists'; }

# create alm
for f in ${chart}/*.yaml
do
	kubectl replace --force -f ${f}
done

# wait for deployments to be ready (loop can be removed when rollout status -w actually works)
n=0
until [ $n -ge 5 ]
do
  kubectl rollout status -w deployment/alm-operator --namespace=${namespace} && break
  n=$[$n+1]
  sleep 1
done

n=0
until [ $n -ge 5 ]
do
  kubectl rollout status -w deployment/catalog-operator --namespace=${namespace} && break
  n=$[$n+1]
  sleep 1
done

n=0
until [ $n -ge 5 ]
do
  kubectl rollout status -w deployment/alm-service-broker --namespace=${namespace} && break
  n=$[$n+1]
  sleep 1
done
