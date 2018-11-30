#!/usr/bin/env bash

# Note: run from root
# Individual tests can be run by calling ./test/e2e/run_e2e_local.sh TestName

set -e

timestamp=$(date +%s)
namespace="e2e-tests-${timestamp}-$RANDOM"

function cleanup {
 	kubectl delete namespace ${namespace}
 	rm -rf test/e2e/resources
}

function cleanupAndExit {
	exitCode=$?
	if [ "$exitCode" -ne "0" ]; then
		echo "error running tests. logs written to package.log";
		kubectl -n ${namespace} logs -l app=package-server > package.log
	else
		cleanup
	fi

    exit $exitCode
}

trap cleanupAndExit SIGINT SIGTERM EXIT


kubectl create namespace ${namespace}

./scripts/package-release.sh 1.0.0-e2e test/e2e/resources test/e2e/e2e-bare-values.yaml
./scripts/install_bare.sh ${namespace} test/e2e/resources

# run tests
if [ -z "$1" ]; then
  test_flags="";
else
  test_flags="-test.run ${1}"
fi

echo "${test_flags}"
go test -tags=bare -mod=vendor -covermode=count -coverpkg ./pkg/controller/...  -test.v -test.timeout 20m ${test_flags} ./test/e2e/... -kubeconfig=${KUBECONFIG:-~/.kube/config} -namespace=${namespace}
