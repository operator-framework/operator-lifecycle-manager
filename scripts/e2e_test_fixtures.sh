#!/usr/bin/env bash

# Default values
OPM_VERSION=$(go list -m github.com/operator-framework/operator-registry | cut -d" " -f2 | sed 's/^v//')
PUSH=false
CONTAINER_RUNTIME=docker
REGISTRY=quay.io/olmtest
TARGET_BRANCH=master
JUST_CHECK=false

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
    # check if images need to be updated - won't build or push images
    --check)
      JUST_CHECK="true"
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

    *)
      printf "*************************\n"
      printf "* Error: Invalid argument.\n"
      # shellcheck disable=SC2059
      printf "* Usage: %s [--opm-version=version] [--push=true|false] [--container-runtime=runtime] [--registry=registry] [--target-branch=branch]\n" "$0"
      printf "*************************\n"
      exit 1
  esac
  shift
done

function check_changes() {
  OPM_CHANGED=false
  FIXTURES_CHANGED=false

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

function push_fixtures() {
  ${CONTAINER_RUNTIME} push "${TEST_CATALOG_IMAGE}"
}

if [ "$JUST_CHECK" = true ]; then
  check_changes
  exit 0
fi

# Fixtures
BUNDLE_V1_IMAGE="${REGISTRY}/busybox-bundle:1.0.0-${OPM_VERSION}"
BUNDLE_V1_DEP_IMAGE="${REGISTRY}/busybox-dependency-bundle:1.0.0-${OPM_VERSION}"
BUNDLE_V2_IMAGE="${REGISTRY}/busybox-bundle:2.0.0-${OPM_VERSION}"
BUNDLE_V2_DEP_IMAGE="${REGISTRY}/busybox-dependency-bundle:2.0.0-${OPM_VERSION}"

INDEX_V1="${REGISTRY}/busybox-dependencies-index:1.0.0-with-ListBundles-method-${OPM_VERSION}"
INDEX_V2="${REGISTRY}/busybox-dependencies-index:2.0.0-with-ListBundles-method-${OPM_VERSION}"

TEST_CATALOG_IMAGE="${REGISTRY}/test-catalog:${OPM_VERSION}"

# Busybox Operator Index Image
${CONTAINER_RUNTIME} build -t "${BUNDLE_V1_IMAGE}" ./test/images/busybox-index/busybox/1.0.0
${CONTAINER_RUNTIME} build -t "${BUNDLE_V1_DEP_IMAGE}" ./test/images/busybox-index/busybox-dependency/1.0.0
${CONTAINER_RUNTIME} build -t "${BUNDLE_V2_IMAGE}" ./test/images/busybox-index/busybox/2.0.0
${CONTAINER_RUNTIME} build -t "${BUNDLE_V2_DEP_IMAGE}" ./test/images/busybox-index/busybox-dependency/2.0.0

# Build catalog from templates
mkdir -p ./test/images/busybox-index/indexv1
mkdir -p ./test/images/busybox-index/indexv2
sed -e "s|#BUNDLE_V1_IMAGE#|\"${BUNDLE_V1_IMAGE}\"|g" -e "s|#BUNDLE_V1_DEP_IMAGE#|\"${BUNDLE_V1_DEP_IMAGE}\"|g" ./test/images/busybox-index/busybox-index-v1.template.json > ./test/images/busybox-index/indexv1/catalog.json
sed -e "s|#BUNDLE_V1_IMAGE#|\"${BUNDLE_V1_IMAGE}\"|g" -e "s|#BUNDLE_V1_DEP_IMAGE#|\"${BUNDLE_V1_DEP_IMAGE}\"|g" -e "s|#BUNDLE_V2_IMAGE#|\"${BUNDLE_V2_IMAGE}\"|g" -e "s|#BUNDLE_V2_DEP_IMAGE#|\"${BUNDLE_V2_DEP_IMAGE}\"|g" ./test/images/busybox-index/busybox-index-v2.template.json > ./test/images/busybox-index/indexv2/catalog.json

# Clean up
rm -rf ./test/images/busybox-index/indexv1
rm -rf ./test/images/busybox-index/indexv2

# Test catalog used for e2e tests related to serving an extracted registry
# Let's reuse one of the other indices for this
${CONTAINER_RUNTIME} tag -t "${TEST_CATALOG_IMAGE}" "${INDEX_V2}"

if [ "$PUSH" = true ]; then
  # push bundles
    ${CONTAINER_RUNTIME} push "${BUNDLE_V1_IMAGE}"
    ${CONTAINER_RUNTIME} push "${BUNDLE_V1_IMAGE}"
    ${CONTAINER_RUNTIME} push "${BUNDLE_V1_IMAGE}"
    ${CONTAINER_RUNTIME} push "${BUNDLE_V1_IMAGE}"

    # push indexes
    ${CONTAINER_RUNTIME} push "${INDEX_V1}"
    ${CONTAINER_RUNTIME} push "${INDEX_V2}"

    # push test catalog
fi
