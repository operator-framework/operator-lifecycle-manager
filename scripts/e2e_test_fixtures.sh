#!/usr/bin/env bash

# Load bingo tools for kind
source .bingo/variables.env

# Default values
OPM_VERSION=$(go list -m github.com/operator-framework/operator-registry | cut -d" " -f2 | sed 's/^v//')

# Parameters
KIND=${KIND:-kind}
KIND_CLUSTER_NAME=${KIND_CLUSTER_NAME:-kind-olmv0}
CONTAINER_RUNTIME=${CONTAINER_RUNTIME:-docker}
REGISTRY=${REGISTRY:-quay.io/olmtest}
TARGET_BRANCH=${TARGET_BRANCH:-master}
PUSH_TO=${PUSH_TO:-quay.io/olmtest}

# Flags
CHECK=false
LOAD_KIND=false
BUILD=true
PUSH=false
SAVE=false

while [ $# -gt 0 ]; do
  case "$1" in
    # opm version to build the fixtures against, e.g. 1.39.0
    --opm-version=*)
      OPM_VERSION="${1#*=}"
      ;;
    # push images to registry after build
    --push)
      PUSH="true"
      ;;
    # push images to a different registry
    --push-to=*)
      PUSH_TO="${1#*=}"
      PUSH="true"
      ;;
    # check if images need to be updated - won't build or push images
    --check)
      CHECK="true"
      ;;
    # container runtime to use, e.g. podman (default docker)
    --container-runtime=*)
      CONTAINER_RUNTIME="${1#*=}"
      ;;
    # registry to push images to, e.g. quay.io/olmtest
    --registry=*)
      REGISTRY="${1#*=}"
      ;;
    # target branch to compare against when checking for changes, e.g. master
    --target-branch=*)
      TARGET_BRANCH="${1#*=}"
      ;;
    --kind-load)
      LOAD_KIND="true"
      ;;
    --save)
      SAVE="true"
      ;;
    --skip-build)
      BUILD="false"
      ;;
    *)
      printf "*************************\n"
      printf "* Error: Invalid argument.\n"
      printf "* Usage: %s [--opm-version=version] [--check] [--push] [--container-runtime=runtime] [--registry=registry] [--target-branch=branch] [--kind-load] [--save] [--skip-build] \n" "$0"
      printf "\n"
      printf "\t--opm-version: opm version to build the fixtures against, e.g. 1.39.0\n"
      printf "\t--check: check if images need to be updated - won't build or push images\n"
      printf "\t--push: push images to registry after build\n"
      printf "\t--container-runtime: container runtime to use, e.g. podman (default docker)\n"
      printf "\t--registry: registry to push images (default: quay.io/olmtest)\n"
      printf "\t--target-branch: target branch to compare against when checking for changes (default: master)\n"
      printf "\t--kind-load: load fixture images into kind cluster (default: false)\n"
      printf "\t--save: save images to tar.gz files (default: false)\n"
      printf "\t--skip-build: skip building images - useful if you just want to kind-load/save/push (default: false)\n"

      printf "*************************\n"
      exit 1
  esac
  shift
done

function check_changes() {
  OPM_CHANGED=false
  FIXTURES_CHANGED=false

  git fetch origin "${TARGET_BRANCH}" --depth=2
  if git diff "origin/${TARGET_BRANCH}" -- go.mod | grep -E '^\+[[:space:]]+github.com/operator-framework/operator-registry' > /dev/null; then
    OPM_CHANGED=true
  fi

  if git diff "origin/${TARGET_BRANCH}" -- test/images scripts/build_test_images.sh > /dev/null; then
    FIXTURES_CHANGED=true
  fi

  if [ "$OPM_CHANGED" = true ] || [ "$FIXTURES_CHANGED" = true ]; then
    echo "true"
  else
    echo "false"
  fi
}

set -x

# Fixtures
BUNDLE_V1_IMAGE="${REGISTRY}/busybox-bundle:1.0.0-${OPM_VERSION}"
BUNDLE_V1_DEP_IMAGE="${REGISTRY}/busybox-dependency-bundle:1.0.0-${OPM_VERSION}"
BUNDLE_V2_IMAGE="${REGISTRY}/busybox-bundle:2.0.0-${OPM_VERSION}"
BUNDLE_V2_DEP_IMAGE="${REGISTRY}/busybox-dependency-bundle:2.0.0-${OPM_VERSION}"

INDEX_V1="${REGISTRY}/busybox-dependencies-index:1.0.0-with-ListBundles-method-${OPM_VERSION}"
INDEX_V2="${REGISTRY}/busybox-dependencies-index:2.0.0-with-ListBundles-method-${OPM_VERSION}"

TEST_CATALOG_IMAGE="${REGISTRY}/test-catalog:${OPM_VERSION}"

# Prints true if changes are detected, false otherwise
if [ "$CHECK" = "true" ]; then
  check_changes
  exit 0
fi

if [ "$BUILD" = "true" ]; then
  # Busybox Operator
  # Build bundles
  ${CONTAINER_RUNTIME} build -t "${BUNDLE_V1_IMAGE}" ./test/images/busybox-index/busybox/1.0.0
  ${CONTAINER_RUNTIME} build -t "${BUNDLE_V1_DEP_IMAGE}" ./test/images/busybox-index/busybox-dependency/1.0.0
  ${CONTAINER_RUNTIME} build -t "${BUNDLE_V2_IMAGE}" ./test/images/busybox-index/busybox/2.0.0
  ${CONTAINER_RUNTIME} build -t "${BUNDLE_V2_DEP_IMAGE}" ./test/images/busybox-index/busybox-dependency/2.0.0


  # Build catalogs
  ${CONTAINER_RUNTIME} build -t "${INDEX_V1}" --build-arg="OPM_VERSION=v${OPM_VERSION}" --build-arg="CONFIGS_DIR=indexv1" ./test/images/busybox-index
  ${CONTAINER_RUNTIME} build -t "${INDEX_V2}" --build-arg="OPM_VERSION=v${OPM_VERSION}" --build-arg="CONFIGS_DIR=indexv2" ./test/images/busybox-index

  # The following catalog used for e2e tests related to serving an extracted registry
  # See catalog_e2e_test.go
  # let's just reuse one of the other catalogs for this - the tests don't care about the content
  # only that a catalog's content can be extracted and served by a different container
  ${CONTAINER_RUNTIME} tag "${INDEX_V2}" "${TEST_CATALOG_IMAGE}"
fi

# Assumes images are already built, kind cluster is running, and kubeconfig is set
if [ "$LOAD_KIND" = true ]; then
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${BUNDLE_V1_IMAGE}"
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${BUNDLE_V1_DEP_IMAGE}"
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${BUNDLE_V2_IMAGE}"
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${BUNDLE_V2_DEP_IMAGE}"
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${INDEX_V1}"
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${INDEX_V2}"
  ${KIND} load docker-image --name="${KIND_CLUSTER_NAME}" "${TEST_CATALOG_IMAGE}"
fi

# Assumes images are already built
if [ "${SAVE}" = true ]; then
  ${CONTAINER_RUNTIME} save "${BUNDLE_V1_IMAGE}" | gzip > bundlev1.tar.gz
  ${CONTAINER_RUNTIME} save "${BUNDLE_V1_DEP_IMAGE}" | gzip > bundlev1dep.tar.gz

  ${CONTAINER_RUNTIME} save "${BUNDLE_V2_IMAGE}" | gzip > bundlev2.tar.gz
  ${CONTAINER_RUNTIME} save "${BUNDLE_V2_DEP_IMAGE}" | gzip > bundlev2dep.tar.gz

  ${CONTAINER_RUNTIME} save "${INDEX_V1}" | gzip > indexv1.tar.gz
  ${CONTAINER_RUNTIME} save "${INDEX_V2}" | gzip > indexv2.tar.gz

  ${CONTAINER_RUNTIME} save "${TEST_CATALOG_IMAGE}" | gzip > testcatalog.tar.gz
fi

# Assumes images are already built
if [ "$PUSH" = true ]; then
  if [ ! "$PUSH_TO" = "" ]; then
    ${CONTAINER_RUNTIME} tag "${BUNDLE_V1_IMAGE}" "${PUSH_TO}/busybox-bundle:1.0.0-${OPM_VERSION}"
    ${CONTAINER_RUNTIME} tag "${BUNDLE_V1_DEP_IMAGE}" "${PUSH_TO}/busybox-dependency-bundle:1.0.0-${OPM_VERSION}"
    ${CONTAINER_RUNTIME} tag "${BUNDLE_V2_IMAGE}" "${PUSH_TO}/busybox-bundle:2.0.0-${OPM_VERSION}"
    ${CONTAINER_RUNTIME} tag "${BUNDLE_V2_DEP_IMAGE}" "${PUSH_TO}/busybox-dependency-bundle:2.0.0-${OPM_VERSION}"
    ${CONTAINER_RUNTIME} tag "${INDEX_V1}" "${PUSH_TO}/busybox-dependencies-index:1.0.0-with-ListBundles-method-${OPM_VERSION}"
    ${CONTAINER_RUNTIME} tag "${INDEX_V2}" "${PUSH_TO}/busybox-dependencies-index:2.0.0-with-ListBundles-method-${OPM_VERSION}"
    ${CONTAINER_RUNTIME} tag "${TEST_CATALOG_IMAGE}" "${PUSH_TO}/test-catalog:${OPM_VERSION}"

    BUNDLE_V1_IMAGE="${PUSH_TO}/busybox-bundle:1.0.0-${OPM_VERSION}"
    BUNDLE_V1_DEP_IMAGE="${PUSH_TO}/busybox-dependency-bundle:1.0.0-${OPM_VERSION}"
    BUNDLE_V2_IMAGE="${PUSH_TO}/busybox-bundle:2.0.0-${OPM_VERSION}"
    BUNDLE_V2_DEP_IMAGE="${PUSH_TO}/busybox-dependency-bundle:2.0.0-${OPM_VERSION}"
    INDEX_V1="${PUSH_TO}/busybox-dependencies-index:1.0.0-with-ListBundles-method-${OPM_VERSION}"
    INDEX_V2="${PUSH_TO}/busybox-dependencies-index:2.0.0-with-ListBundles-method-${OPM_VERSION}"
    TEST_CATALOG_IMAGE="${PUSH_TO}/test-catalog:${OPM_VERSION}"
  fi
  # push bundles
  ${CONTAINER_RUNTIME} push "${BUNDLE_V1_IMAGE}"
  ${CONTAINER_RUNTIME} push "${BUNDLE_V1_DEP_IMAGE}"
  ${CONTAINER_RUNTIME} push "${BUNDLE_V2_IMAGE}"
  ${CONTAINER_RUNTIME} push "${BUNDLE_V2_DEP_IMAGE}"

  # push indexes
  ${CONTAINER_RUNTIME} push "${INDEX_V1}"
  ${CONTAINER_RUNTIME} push "${INDEX_V2}"

  # push test catalog
  ${CONTAINER_RUNTIME} push "${TEST_CATALOG_IMAGE}"
fi
