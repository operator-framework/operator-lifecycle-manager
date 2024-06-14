#! /bin/bash

set -o errexit
set -o nounset
set -o pipefail

help="
build-push-e2e-catalog.sh is a script to build and push the e2e catalog image using kaniko.
Usage:
  build-push-e2e-catalog.sh [NAMESPACE] [TAG]

Argument Descriptions:
  - NAMESPACE is the namespace the kaniko Job should be created in
  - TAG is the full tag used to build and push the catalog image
"

if [[ "$#" -ne 2 ]]; then
  echo "Illegal number of arguments passed"
  echo "${help}"
  exit 1
fi

namespace=$1
tag=$2

OPM_VERSION=${OPM_VERSION:-"latest"}

echo "${namespace}" "${tag}"

# Delete existing configmaps
kubectl delete configmap -n "${namespace}" test-catalog.dockerfile --ignore-not-found
kubectl delete configmap -n "${namespace}" test-catalog.build-contents --ignore-not-found

kubectl create configmap -n "${namespace}" --from-file=test/images/test-catalog/dockerfile test-catalog.dockerfile
kubectl create configmap -n "${namespace}" --from-file=test/images/test-catalog/configs test-catalog.build-contents

kubectl apply -f - << EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: kaniko
  namespace: "${namespace}"
spec:
  template:
    spec:
      containers:
      - name: kaniko
        image: gcr.io/kaniko-project/executor:latest
        args: [ "--build-arg=OPM_VERSION=${OPM_VERSION}",
                "--dockerfile=/workspace/dockerfile",
                "--context=/workspace",
                "--destination=${tag}",
                "--verbosity=trace",
                "--skip-tls-verify"]
        volumeMounts:
          - name: dockerfile
            mountPath: /workspace/
          - name: build-contents
            mountPath: /workspace/configs/
      restartPolicy: Never
      volumes:
        - name: dockerfile
          configMap:
            name: test-catalog.dockerfile
            items:
              - key: dockerfile
                path: dockerfile
        - name: build-contents
          configMap:
            name: test-catalog.build-contents
EOF

kubectl wait --for=condition=Complete -n "${namespace}" jobs/kaniko --timeout=60s