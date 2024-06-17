#! /bin/bash

set -x
set -o errexit
set -o nounset
set -o pipefail

src=$1
name=$2
namespace=$3
dest=$4

OPM_VERSION=${OPM_VERSION:-"latest"}

# Delete existing configmaps
kubectl delete configmap -n "${namespace}" "${name}.dockerfile" --ignore-not-found
kubectl delete configmap -n "${namespace}" "${name}.build-contents" --ignore-not-found

kubectl create configmap -n "${namespace}" --from-file="${src}/dockerfile" "${name}.dockerfile"
kubectl create configmap -n "${namespace}" --from-file="${src}/configs" "${name}.build-contents"

# Create the kaniko job
kubectl apply -f - << EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: "kaniko-${name}"
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
                "--destination=${dest}",
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
            name: "${name}.dockerfile"
            items:
              - key: dockerfile
                path: dockerfile
        - name: build-contents
          configMap:
            name: "${name}.build-contents"
EOF

kubectl wait --for=condition=Complete -n "${namespace}" "jobs/kaniko-${name}" --timeout=60s