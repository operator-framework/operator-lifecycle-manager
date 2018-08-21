#!/usr/bin/env bash

if ! docker pull "quay.io/coreos/olm:$1-rhel" || ! docker pull "quay.io/coreos/catalog:$1-rhel"; then
  docker build -t "quay.io/coreos/olm:$1-rhel" -t "quay.io/coreos/catalog:$1-rhel" .
  docker push "quay.io/coreos/olm:$1-rhel"
  docker push "quay.io/coreos/catalog:$1-rhel"
fi
