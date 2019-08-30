#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

if [ -z "$NO_MINIKUBE" ]; then
  pgrep -f "[m]inikube" >/dev/null || minikube start --kubernetes-version="v1.14.2" --extra-config=apiserver.v=4 || { echo 'Cannot start minikube.'; exit 1; }
  eval "$(minikube docker-env)" || { echo 'Cannot switch to minikube docker'; exit 1; }
  kubectl config use-context minikube
fi

cp local.Dockerfile test/e2e/hang.Dockerfile bin
docker build -f bin/local.Dockerfile -t quay.io/operator-framework/olm:local -t quay.io/operator-framework/olm-e2e:local ./bin
docker build -f bin/hang.Dockerfile -t hang:10 ./bin

if [ -x "$(command -v kind)" ] && [ "$(kubectl config current-context)" = "kind" ]; then
  kind load docker-image quay.io/operator-framework/olm:local
  kind load docker-image quay.io/operator-framework/olm-e2e:local
fi
