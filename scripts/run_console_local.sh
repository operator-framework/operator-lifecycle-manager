#!/usr/bin/env bash

secretname=$(kubectl get serviceaccount default --namespace=kube-system -o jsonpath='{.secrets[0].name}')
endpoint=$(kubectl config view -o json | jq '{myctx: .["current-context"], ctxs: .contexts[], clusters: .clusters[]}' | jq 'select(.myctx == .ctxs.name)' | jq 'select(.ctxs.context.cluster ==  .clusters.name)' | jq '.clusters.cluster.server' -r)

echo "Using $endpoint"
docker run -it -p 9000:9000 \
  -e BRIDGE_USER_AUTH="disabled" \
  -e BRIDGE_K8S_MODE="off-cluster" \
  -e BRIDGE_K8S_MODE_OFF_CLUSTER_ENDPOINT=$endpoint \
  -e BRIDGE_K8S_MODE_OFF_CLUSTER_SKIP_VERIFY_TLS=true \
  -e BRIDGE_K8S_AUTH="bearer-token" \
  -e BRIDGE_K8S_AUTH_BEARER_TOKEN=$(kubectl get secret "$secretname" --namespace=kube-system -o template --template='{{.data.token}}' | base64 --decode) \
  quay.io/openshift/origin-console:latest
