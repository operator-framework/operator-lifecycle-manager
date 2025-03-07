#!/usr/bin/env bash

KIND=${KIND:-kind}
CONTAINER_RUNTIME=${CONTAINER_RUNTIME:-docker}

# Default values
OPERATOR_REGISTRY_VERSION="${OPERATOR_REGISTRY_VERSION:-$(go list -m github.com/operator-framework/operator-registry | cut -d" " -f2 | sed 's/^v//')}"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-kind-olmv0}"
REGISTRY="${REGISTRY:-localhost:5001}"

# Fixtures
# Note: the following catalogs reference bundles stored in quay.io/olmtest
INDEX_V1="${REGISTRY}/busybox-dependencies-index:1.0.0-with-ListBundles-method-${OPM_VERSION}"
INDEX_V2="${REGISTRY}/busybox-dependencies-index:2.0.0-with-ListBundles-method-${OPM_VERSION}"
TEST_CATALOG_IMAGE="${REGISTRY}/test-catalog:e2e"

## Build
${CONTAINER_RUNTIME} build -t "${INDEX_V1}" --build-arg="OPM_VERSION=v${OPERATOR_REGISTRY_VERSION}" -f ./test/images/busybox-index/index.Dockerfile ./test/images/busybox-index/indexv1
${CONTAINER_RUNTIME} build -t "${INDEX_V2}" --build-arg="OPM_VERSION=v${OPERATOR_REGISTRY_VERSION}" -f ./test/images/busybox-index/index.Dockerfile ./test/images/busybox-index/indexv2

# The following catalog used for e2e tests related to serving an extracted registry
# See catalog_e2e_test.go
# let's just reuse one of the other catalogs for this - the tests don't care about the content
# only that a catalog's content can be extracted and served by a different container
${CONTAINER_RUNTIME} tag "${INDEX_V2}" "${TEST_CATALOG_IMAGE}"

### Push
${CONTAINER_RUNTIME} push "${INDEX_V1}"
${CONTAINER_RUNTIME} push "${INDEX_V2}"
${CONTAINER_RUNTIME} push "${TEST_CATALOG_IMAGE}"
