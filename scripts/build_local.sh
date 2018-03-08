#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

minikube start --extra-config=apiserver.Authorization.Mode=RBAC || { echo 'Cannot start minikube.'; exit 1; }
eval $(minikube docker-env) || { echo 'Cannot switch to minikube docker'; exit 1; }
kubectl config use-context minikube
docker build \
       -t quay.io/coreos/catalog:local \
       -t quay.io/coreos/olm:local \
       -t quay.io/coreos/olm-service-broker:local \
       -f e2e-local-build.Dockerfile .
