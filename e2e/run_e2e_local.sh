#!/usr/bin/env bash

# Note: run from root

set -e

timestamp=$(date +%s)
namespace="e2e-tests-${timestamp}-$RANDOM"
charttmpdir=`mktemp -d 2>/dev/null || mktemp -d -t 'charttmpdir'`
mkdir ${charttmpdir}/alm-app
charttmpdir=${charttmpdir}/alm-app

function cleanup {
 	kubectl delete namespace ${namespace}
 	rm -rf ${charttmpdir}
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

# use minikube context
kubectl config use-context minikube
kubectl apply -f ./Documentation/install/minikube/minikube_kube-system_fix.yaml
eval $(minikube docker-env) || { echo 'Cannot switch to minikube docker'; exit 1; }

# initialize helm
helm init; kubectl rollout status -w deployment/tiller-deploy --namespace=kube-system || { echo 'Cannot initialize Helm.'; exit 1; }

# create alm NS and CRDs
kubectl create ns ${namespace} || { echo 'ns exists'; }
kubectl apply -f ./Documentation/install/alm_resources/clusterserviceversion.crd.yaml || { echo 'clusterserviceversion crd exists'; }
kubectl apply -f ./Documentation/install/alm_resources/installplan.crd.yaml || { echo 'installplan crd exists'; }
kubectl apply -f ./Documentation/install/alm_resources/alphacatalogentry.crd.yaml || { echo 'alphacatalogentry crd exists'; }

# copy chart and add version
cp -R deploy/alm-app/kube-1.8/ ${charttmpdir}/
echo "version: 1.0.0-${namespace}" >> ${charttmpdir}/Chart.yaml

# Install ALM and wait for it to be ready
helm install --wait --timeout 300 -f e2e/e2e_values.yaml -n ${namespace} --set namespace=${namespace} --set catalog_namespace=${namespace} ${charttmpdir}

# run tests
KUBECONFIG=~/.kube/config NAMESPACE=${namespace} go test -v ./e2e/...
