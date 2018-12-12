#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

if [ -z "$NO_MINIKUBE" ]; then
  ps x | grep -q [m]inikube || minikube start --kubernetes-version="v1.11.0" --extra-config=apiserver.v=4 || { echo 'Cannot start minikube.'; exit 1; }
  eval $(minikube docker-env) || { echo 'Cannot switch to minikube docker'; exit 1; }
  kubectl config use-context minikube
  umask 0077 && kubectl config view --minify --flatten --context=minikube > minikube.kubeconfig
fi

kubectl delete crds --all
kubectl create namespace $(cat $(pwd)/e2e.namespace)
