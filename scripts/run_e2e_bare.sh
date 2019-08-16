#!/usr/bin/env bash

# Note: run from root
# Individual tests can be run by calling ./test/e2e/run_e2e_bare.sh TestName

set -e

# run tests
if [ -z "$1" ]; then
  test_flags="";
else
  test_flags="-test.run ${1}"
fi

echo "${test_flags}"
go test -c -tags=bare -mod=vendor -v -o e2e-bare github.com/operator-framework/operator-lifecycle-manager/test/e2e
./e2e-bare  -test.v -test.timeout 20m ${test_flags} -kubeconfig=${KUBECONFIG:-minikube.kubeconfig} -namespace=$(cat e2e.namespace) -olmNamespace=operator-lifecycle-manager -dummyImage=hang:10
