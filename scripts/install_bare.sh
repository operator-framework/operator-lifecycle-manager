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

# create OLM resources, minus the running components (they will run locally)
for f in ${chart}/*.yaml
do
    if [[ $f == *.configmap.yaml ]]
    then
        kubectl replace --force -f ${f};
    elif [[ $f == *.deployment.yaml ]]
    then
    	# skip olm and catalog operator deployment
    	continue
    else
        kubectl apply -f ${f};
    fi
done
