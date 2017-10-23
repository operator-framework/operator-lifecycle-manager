#! /bin/bash

minikube start --extra-config=apiserver.Authorization.Mode=RBAC
kubectl create ns tectonic-system
kubectl apply -f ./minikube/minikube_kube-system_fix.yaml
kubectl apply -f ./alm_resources
