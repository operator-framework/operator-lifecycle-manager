#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

kubectl delete crds --all
kubectl create namespace $(cat $(pwd)/e2e.namespace)
