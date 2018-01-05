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
 	rm -rf e2e/test-resources
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

mkdir -p e2e/test-resources

pushd e2e/chart/templates
filenames=$(ls *.yaml)
popd

for f in ${filenames}
do
  echo "Processing $f file..."
  helm template --set namespace=${namespace} -f e2e/e2e-values.yaml -x templates/${f} e2e/chart > e2e/test-resources/${f}
done

eval $(minikube docker-env) || { echo 'Cannot switch to minikube docker'; exit 1; }
docker build -t quay.io/coreos/alm-e2e:local -f e2e-local-run.Dockerfile .
kubectl apply -f e2e/test-resources
until kubectl -n ${namespace} logs job/e2e | grep -v "ContainerCreating"; do echo "waiting for job to run" && sleep 1; done
kubectl -n ${namespace} logs job/e2e -f


