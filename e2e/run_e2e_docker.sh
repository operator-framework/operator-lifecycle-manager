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
# Add a rolebinding for the test runner to use
helm template --set namespace=${namespace} -x templates/e2e-rolebinding.yaml e2e/chart > e2e/resources/e2e-rolebinding.yaml

function cleanup {
 	kubectl delete namespace ${namespace}
 	rm -rf e2e/resources
}

function cleanupAndExit {
	exitCode=$?
	if [ "$exitCode" -ne "0" ]; then
		echo "error running tests, printing pod logs: ";
		kubectl -n ${namespace} logs -l app=alm;
	fi
	cleanup
    exit $exitCode
}

trap cleanupAndExit SIGINT SIGTERM EXIT

./Documentation/install/install_local.sh ${namespace} e2e/resources

eval $(minikube docker-env) || { echo 'Cannot switch to minikube docker'; exit 1; }
docker build -t quay.io/coreos/alm-e2e:local -f e2e-local-run.Dockerfile .
kubectl create -n ${namespace} -f e2e/job.yaml
until kubectl -n ${namespace} logs job/e2e | grep -v "ContainerCreating"; do echo "waiting for job to run" && sleep 1; done
kubectl -n ${namespace} logs job/e2e -f


