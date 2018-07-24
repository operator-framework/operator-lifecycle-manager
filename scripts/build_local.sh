#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

minikube start || { echo 'Cannot start minikube.'; exit 1; }
eval $(minikube docker-env) || { echo 'Cannot switch to minikube docker'; exit 1; }
kubectl config use-context minikube
docker build -f e2e.Dockerfile .
docker tag $(docker images --filter 'label=broker=true' --format '{{.CreatedAt}}\t{{.ID}}' | sort -nr | head -n 1 | cut -f2) quay.io/coreos/olm-service-broker:local
docker tag $(docker images --filter 'label=catalog=true' --format '{{.CreatedAt}}\t{{.ID}}' | sort -nr | head -n 1 | cut -f2) quay.io/coreos/catalog:local
docker tag $(docker images --filter 'label=e2e=true' --format '{{.CreatedAt}}\t{{.ID}}' | sort -nr | head -n 1 | cut -f2) quay.io/coreos/olm-e2e:local
docker tag $(docker images --filter 'label=olm=true' --format '{{.CreatedAt}}\t{{.ID}}' | sort -nr | head -n 1 | cut -f2) quay.io/coreos/olm:local