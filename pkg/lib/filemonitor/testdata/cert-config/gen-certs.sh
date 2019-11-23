#!/bin/bash
# Based off:
# https://kubernetes.io/docs/concepts/cluster-administration/certificates/
#
# This scripts generates self-signed certificate keypairs, with the only
# difference being that the subjects are different (set in $CSR) to easily allow
# detecting which are in use.

function set_variables {
  MASTER_IP="127.0.0.1"
  CA_CRT=ca.crt
  CA_KEY=ca.key
  CSR=csr-$SUFFIX.conf
  SERVER_CSR=server-$SUFFIX.csr
  SERVER_CRT=server-$SUFFIX.crt
  SERVER_KEY=server-$SUFFIX.key
}

function generate_ca {
  openssl genrsa -out $CA_KEY 2048
  openssl req -x509 -new -nodes -key $CA_KEY -subj "/CN=${MASTER_IP}" -days 10000 -out $CA_CRT
}

function generate_certs {
  echo "Generating certs for $SUFFIX"
  openssl genrsa -out "$SERVER_KEY" 2048
  openssl req -new -key "$SERVER_KEY" -out "$SERVER_CSR" -config "$CSR"
  openssl x509 -req -in "$SERVER_CSR" -CA $CA_CRT -CAkey "$CA_KEY" -CAcreateserial -out "$SERVER_CRT" -days 10000 -extensions v3_ext -extfile "$CSR"
  #openssl x509  -noout -text -in "$SERVER_CRT"
  echo "---"
}


SUFFIX=old
set_variables
generate_ca # do this only once
generate_certs

SUFFIX=new
set_variables
generate_certs
