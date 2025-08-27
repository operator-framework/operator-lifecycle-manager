#!/usr/bin/env bash

QUAY_REPO="${QUAY_REPO:-olmtest}"
SINGLE_BUNDLE_TAG="${BUNDLE_TAG:-objects-pdb-v2}"
SINGLE_BUNDLE_INDEX_TAG="${INDEX_TAG:-pdb-v2}"

# Busybox Operator Index Image
docker build -t quay.io/${QUAY_REPO}/busybox-bundle:1.0.0 ./test/images/busybox-index/busybox/1.0.0
docker build -t quay.io/${QUAY_REPO}/busybox-bundle:2.0.0 ./test/images/busybox-index/busybox/2.0.0

docker build -t quay.io/${QUAY_REPO}/busybox-dependency-bundle:1.0.0 ./test/images/busybox-index/busybox-dependency/1.0.0
docker build -t quay.io/${QUAY_REPO}/busybox-dependency-bundle:2.0.0 ./test/images/busybox-index/busybox-dependency/2.0.0

docker push quay.io/${QUAY_REPO}/busybox-bundle:1.0.0
docker push quay.io/${QUAY_REPO}/busybox-bundle:2.0.0
docker push quay.io/${QUAY_REPO}/busybox-dependency-bundle:1.0.0
docker push quay.io/${QUAY_REPO}/busybox-dependency-bundle:2.0.0

opm index add --bundles quay.io/${QUAY_REPO}/busybox-dependency-bundle:1.0.0,quay.io/olmtest/busybox-bundle:1.0.0 --tag quay.io/olmtest/busybox-dependencies-index:1.0.0-with-ListBundles-method -c docker
docker push quay.io/${QUAY_REPO}/busybox-dependencies-index:1.0.0-with-ListBundles-method

opm index add --bundles quay.io/${QUAY_REPO}/busybox-dependency-bundle:2.0.0,quay.io/olmtest/busybox-bundle:2.0.0 --tag quay.io/olmtest/busybox-dependencies-index:2.0.0-with-ListBundles-method --from-index quay.io/olmtest/busybox-dependencies-index:1.0.0-with-ListBundles-method -c docker
docker push quay.io/${QUAY_REPO}/busybox-dependencies-index:2.0.0-with-ListBundles-method

# Single Bundle E2E Test Image
docker build -t quay.io/${QUAY_REPO}/bundle:${BUNDLE_TAG} ./test/images/single-bundle-index
docker push quay.io/${QUAY_REPO}/bundle:${BUNDLE_TAG}

opm index add --bundles quay.io/${QUAY_REPO}/bundle:${BUNDLE_TAG} --tag quay.io/${QUAY_REPO}/single-bundle-index:${INDEX_TAG} -c docker
docker push quay.io/${QUAY_REPO}/single-bundle-index:${INDEX_TAG}
