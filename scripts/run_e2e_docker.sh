#!/usr/bin/env bash

# Note: run from root

set -e

timestamp=$(date +%s)
namespace="e2e-tests-${timestamp}-$RANDOM"
operator_namespace="$namespace-operator"

tmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t 'valuetmpdir')
test_e2e_config=${tmpdir}/e2e-values.yaml
cp test/e2e/e2e-values.yaml "$test_e2e_config"

{ echo "namespace: ${namespace}";
  echo "watchedNamespaces: \"\"";
  echo "catalog_namespace: ${namespace}";
  echo "operator_namespace: ${operator_namespace}"; }  >> "$test_e2e_config"

./scripts/package_release.sh 1.0.0 test/e2e/resources "$test_e2e_config"

function cleanup {
	for resource in test/e2e/test-resources/*.yaml; do
		[ -e "$resource" ] || continue
		echo "Running kubectl delete -f $resource..."
		kubectl delete -f "$resource" &> /dev/null || continue
	done
	rm -rf test/e2e/resources
	rm -rf test/e2e/test-resources
}

function cleanupAndExit {
	exitCode=$?
	if [ "$exitCode" -ne "0" ]; then
		echo "error running tests. logs written to olm.log and catalog.log";
		kubectl -n "${namespace}" logs -l app=alm-operator > olm.log;
		kubectl -n "${namespace}" logs -l app=catalog-operator > catalog.log;
		kubectl -n "${namespace}" logs -l app=package-server > package.log
	fi
	cleanup
    exit $exitCode
}

trap cleanupAndExit SIGINT SIGTERM EXIT

./scripts/install_local.sh "${namespace}" test/e2e/resources

mkdir -p test/e2e/test-resources
helm template --set namespace="${namespace}"  -f test/e2e/e2e-values.yaml test/e2e/chart  --output-dir test/e2e/test-resources

eval "$(minikube docker-env)" || { echo 'Cannot switch to minikube docker'; exit 1; }
kubectl apply -f test/e2e/test-resources/olm-e2e/templates
until kubectl -n "${namespace}" logs job/e2e | grep -v "ContainerCreating"; do echo "waiting for job to run" && sleep 1; done
kubectl -n "${namespace}" logs job/e2e -f


