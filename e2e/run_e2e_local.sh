#!/usr/bin/env bash

# Note: run from root

set -e

timestamp=$(date +%s)
namespace="e2e-tests-${timestamp}-$RANDOM"

tmpdir=`mktemp -d 2>/dev/null || mktemp -d -t 'valuetmpdir'`
cp e2e/e2e-values.yaml ${tmpdir}/e2e-values.yaml

echo "namespace: ${namespace}" >> ${tmpdir}/e2e-values.yaml
echo "watchedNamespaces: ${namespace}" >> ${tmpdir}/e2e-values.yaml
echo "catalog_namespace: ${namespace}" >> ${tmpdir}/e2e-values.yaml

./deploy/tectonic-alm-operator/package-release.sh ver=1.0.0-e2e e2e/resources ${tmpdir}/e2e-values.yaml

function cleanup {
 	kubectl delete namespace ${namespace}
 	rm -rf e2e/resources
}

function cleanupAndExit {
	exitCode=$?
	if [ "$exitCode" -ne "0" ]; then
		echo "error running tests, printing pod logs: ";
		kubectl -n ${namespace} logs -l release=${namespace};
	fi
	cleanup
    exit $exitCode
}

trap cleanupAndExit SIGINT SIGTERM EXIT

./Documentation/install/install_local.sh ${namespace} e2e/resources

KUBECONFIG=~/.kube/config NAMESPACE=${namespace} go test -v ./e2e/... ${1/[[:alnum:]-]*/-run ${1}}