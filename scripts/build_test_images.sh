#!/usr/bin/env bash

set -e

CATALOG_DIR=./test/catalogs
CATALOG_DOCKER=${CATALOG_DIR}/catalog.Dockerfile

# Given an image and a catalog name
# This functions builds the image and pushes it to the repository
function build_and_push() {
  IMG_NAME=$1
  CATALOG_NAME=$2
  docker build -t "${IMG_NAME}" -f "${CATALOG_DOCKER}" "${CATALOG_DIR}/${CATALOG_NAME}"
  docker push "${IMG_NAME}"
}

# olmtest images

# Busybox Operator Index Image
catalogs=( 1.0.0 2.0.0 )
for c in "${catalogs[@]}"; do
  build_and_push "quay.io/olmtest/busybox-dependencies-index:${c}-with-ListBundles-method" "busybox-${c}"
done

# single bundle index
catalogs=( pdb-v1 objects objects-upgrade-samename objects-upgrade-diffname )
for c in "${catalogs[@]}"; do
  build_and_push "quay.io/olmtest/single-bundle-index:${c}" "single-bundle-index-${c}"
done

# catsrc-update-test catalogs
catalogs=( old new related )
for c in "${catalogs[@]}"; do
  build_and_push "quay.io/olmtest/catsrc-update-test:${c}" "catsrc-update-test-${c}"
done

# operator-framework images

# ci-index
build_and_push quay.io/operator-framework/ci-index:latest "ci-index"

# webhook-operator-index
build_and_push quay.io/operator-framework/webhook-operator-index:0.0.3 "webhook-operator-index-0.0.3"
