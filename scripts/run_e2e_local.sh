#!/usr/bin/env bash

# Note: run from root
# Individual tests can be run by calling ./test/e2e/run_e2e_local.sh TestName

set -e

timestamp=$(date +%s)
namespace="e2e-tests-${timestamp}-$RANDOM"

tmpdir=`mktemp -d 2>/dev/null || mktemp -d -t 'valuetmpdir'`
cp test/e2e/e2e-values.yaml ${tmpdir}/e2e-values.yaml

echo "namespace: ${namespace}" >> ${tmpdir}/e2e-values.yaml
echo "watchedNamespaces: ${namespace}" >> ${tmpdir}/e2e-values.yaml
echo "catalog_namespace: ${namespace}" >> ${tmpdir}/e2e-values.yaml

./scripts/package-release.sh ver=1.0.0-e2e test/e2e/resources ${tmpdir}/e2e-values.yaml

function cleanup {
 	kubectl delete namespace ${namespace}
 	rm -rf test/e2e/resources
}

function cleanupAndExit {
	exitCode=$?
	if [ "$exitCode" -ne "0" ]; then
		echo "error running tests, printing pod logs: ";
		kubectl -n ${namespace} logs -l app=alm-operator;
		kubectl -n ${namespace} logs -l app=catalog-operator;
	fi
	cleanup
    exit $exitCode
}

trap cleanupAndExit SIGINT SIGTERM EXIT

./scripts/install_local.sh ${namespace} test/e2e/resources


# run tests
KUBECONFIG=~/.kube/config NAMESPACE=${namespace} go test -v ./test/e2e/... ${1/[[:alnum:]-]*/-run ${1}}
