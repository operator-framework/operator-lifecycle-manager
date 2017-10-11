#! /bin/bash
# before running this script:
# 1. you must add a version to the chart file
# 2. run eval $(minikube docker-env)
# 3. re-build the docker file tagged with local

minikube start --extra-config=apiserver.Authorization.Mode=RBAC
helm init --upgrade
kubectl create ns alm-local
kubectl apply -f ./Documentation/design/resources/apptype.crd.yaml --namespace=ci-alm-staging
kubectl apply -f ./Documentation/design/resources/clusterserviceversion.crd.yaml --namespace=ci-alm-staging
kubectl apply -f ./minikube_kube-system_fix.yaml
helm upgrade --install alm-local --namespace=alm-local --set image.repository=quay.io/coreos/alm --set image.tag=alm-local --set namespace=alm-local --set nameOverride=alm-local --set image.pullPolicy=IfNotPresent --force ./deploy/alm-app