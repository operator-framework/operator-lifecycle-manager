#! /bin/bash

set -o errexit
set -o nounset
set -o pipefail

set -x

help="
image_registry.sh is a script to stand up an image registry within a cluster.
Usage:
  image_registry.sh [NAMESPACE] [NAME]

Argument Descriptions:
  - NAMESPACE is the namespace that should be created and is the namespace in which the image registry will be created
  - NAME is the name that should be used for the image registry Deployment and Service
"

if [[ "$#" -ne 2 ]]; then
  echo "Illegal number of arguments passed"
  echo "${help}"
  exit 1
fi

namespace=$1
name=$2

# Generate self-signed TLS certificate
./scripts/generate_registry_cert.sh "${namespace}" "${name}"

# Read and base64 encode the certificate and key files
CERT_FILE=$(cat "tls.crt" | base64 | tr -d '\n')
KEY_FILE=$(cat "tls.key" | base64 | tr -d '\n')

kubectl apply -f - << EOF
apiVersion: v1
kind: Namespace
metadata:
  name: ${namespace}
---
apiVersion: v1
kind: Secret
metadata:
  name: ${namespace}-registry
  namespace: ${namespace}
type: Opaque
data:
  tls.crt: "${CERT_FILE}"
  tls.key: "${KEY_FILE}"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
  namespace: ${namespace}
  labels:
    app: registry
spec:
  replicas: 1
  selector:
    matchLabels:
      app: registry
  template:
    metadata:
      labels:
        app: registry
    spec:
      containers:
      - name: registry
        image: registry:2
        volumeMounts:
        - name: certs-vol
          mountPath: "/certs"
        env:
        - name: REGISTRY_HTTP_TLS_CERTIFICATE
          value: "/certs/tls.crt"
        - name: REGISTRY_HTTP_TLS_KEY
          value: "/certs/tls.key"
      volumes:
        - name: certs-vol
          secret:
            secretName: ${namespace}-registry
---
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  namespace: ${namespace}
spec:
  selector:
    app: registry
  ports:
  - port: 5000
    targetPort: 5000
EOF

kubectl wait --for=condition=Available -n "${namespace}" "deploy/${name}" --timeout=60s

# Alternatively, just generate the pair once and save it to the repo. But then in 10 years we might need to generate a new certificate!
rm -rf tls.crt tls.key