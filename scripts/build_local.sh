#!/usr/bin/env bash

# Note: run from root
# This is used to start and build services for running e2e tests

set -e

[ -x "$(command -v kind)" ] && [[ "$(kubectl config current-context)" =~ ^kind-? ]] && KIND=1 NO_MINIKUBE=1

if [ -z "$NO_MINIKUBE" ]; then
  pgrep -f "[m]inikube" >/dev/null || minikube start --kubernetes-version="v1.16.4" --extra-config=apiserver.v=4 || { echo 'Cannot start minikube.'; exit 1; }
  eval "$(minikube docker-env)" || { echo 'Cannot switch to minikube docker'; exit 1; }
  kubectl config use-context minikube
fi

docker build -f e2e.Dockerfile -t quay.io/operator-framework/olm:local -t quay.io/operator-framework/olm-e2e:local ./bin
docker build -f test/e2e/hang.Dockerfile -t hang:10 ./bin

if [ -n "$KIND" ]; then
  CLUSTERS=($(kind get clusters))

  # kind will use the cluster named kind by default, so if there is only one cluster, specify it
  if [[ ${#CLUSTERS[@]} == 1 ]]; then
    KIND_FLAGS="--name ${CLUSTERS[0]}"
    echo 'Use cluster ${CLUSTERS[0]}'
  fi

  kind load docker-image quay.io/operator-framework/olm:local ${KIND_FLAGS}
  kind load docker-image quay.io/operator-framework/olm-e2e:local ${KIND_FLAGS}
  kind load docker-image hang:10 ${KIND_FLAGS}
fi
