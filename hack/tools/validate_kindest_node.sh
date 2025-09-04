#!/bin/bash
# This script verifies that the version of kind used for testing uses a major.minor version of k8s that operator-controller does

# Extract the version of kind, by removing the "${GOBIN}/kind-" prefix
KIND=${KIND#${GOBIN}/kind-}

# Get the version of the image
KIND_VER=$(curl -L -s https://github.com/kubernetes-sigs/kind/raw/refs/tags/${KIND}/pkg/apis/config/defaults/image.go | grep -Eo 'v[0-9]+\.[0-9]+')

# Compare the versions
if [ "${KIND_VER}" != "${K8S_VERSION}" ]; then
    echo "kindest/node:${KIND_VER} version does not match k8s ${K8S_VERSION}"
    exit 1
fi
exit 0
