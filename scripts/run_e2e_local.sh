#!/usr/bin/env bash

# Note: run from root
# Individual tests can be run by calling ./test/e2e/run_e2e_local.sh TestName

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
	for resource in test/e2e/resources/*.yaml; do
		[ -e "$resource" ] || continue
		echo "Running kubectl delete -f $resource..."
		kubectl delete -f "$resource" &> /dev/null || continue
	done
	rm -rf test/e2e/resources
}

function cleanupAndExit {
	exitCode=$?
	if [ "$exitCode" -ne "0" ]; then
		echo "error running tests. logs written to olm.log, catalog.log, and package.log";
		kubectl -n "${namespace}" logs -l app=olm-operator > olm.log;
		kubectl -n "${namespace}" logs -l app=catalog-operator > catalog.log;
		kubectl -n "${namespace}" logs -l app=packageserver > package.log

		# make it obvious if a pod is crashing or has restarted
		kubectl get po --all-namespaces
	else
		cleanup
	fi

    exit $exitCode
}

trap cleanupAndExit SIGINT SIGTERM EXIT

./scripts/install_local.sh "${namespace}" test/e2e/resources

# run tests
if [ -z "$1" ]; then
  test_flags="";
else
  test_flags="-test.run ${1}"
fi

echo "${test_flags}"
go test -mod=vendor -count=1 -failfast -tags=local -covermode=count -coverpkg ./pkg/controller/...  -test.v -test.timeout 30m ${test_flags} ./test/e2e/... -kubeconfig=${KUBECONFIG:-~/.kube/config} -namespace=${namespace}-operator -olmNamespace=${namespace} -dummyImage=hang:10
