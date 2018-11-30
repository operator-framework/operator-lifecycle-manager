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
kubectl delete namespace e2e || true
kubectl wait --for=delete namespace/e2e || true
kubectl create namespace e2e

# only used for package server, other operators run locally
docker build -t quay.io/coreos/olm:local .
