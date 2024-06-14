#!/usr/bin/env bash

set -x

help="
generate_registry_cert.sh is a script to generate the self-signed certificates used by the internal registry.
Usage:
  generate_registry_cert.sh [NAMESPACE] [NAME]

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

# Generate ECDSA private key
openssl ecparam -genkey -name prime256v1 -out tls.key

# Create CSR configuration file (csr.conf)
cat <<EOF > csr.conf
[ req ]
prompt = no
distinguished_name = dn

[ dn ]
CN = ${name}.${namespace}.svc

[ alt_names ]
DNS.1 = ${name}.${namespace}.svc
DNS.2 = ${name}.${namespace}.cluster.local
EOF

# Generate CSR
openssl req -new -key tls.key -out tls.csr -config csr.conf

# Create certificate configuration file (cert.conf)
cat <<EOF > cert.conf
[ req ]
prompt = no
distinguished_name = dn

[ dn ]
CN = ${name}.${namespace}.svc

[ alt_names ]
DNS.1 = ${name}.${namespace}.svc
DNS.2 = ${name}.${namespace}.cluster.local
EOF

# Generate self-signed certificate
openssl req -x509 -key tls.key -in tls.csr -out tls.crt -days 3650 -config cert.conf

# Remove temporary files
rm -rf cert.conf csr.conf tls.csr