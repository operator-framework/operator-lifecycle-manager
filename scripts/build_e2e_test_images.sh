#!/usr/bin/env bash

QUAY_REPO="${QUAY_REPO:-tayler}"
BUNDLE_NAME="${BUNDLE_NAME:-bundle}"
BUNDLE_TAG="${BUNDLE_TAG:-objects-pdb-v1}"
INDEX_NAME="${INDEX_NAME:-single-bundle-index}"
INDEX_TAG="${INDEX_TAG:-pdb-v1}"

# Single Bundle E2E Test Image
docker build -t quay.io/${QUAY_REPO}/${BUNDLE_NAME}:${BUNDLE_TAG} ./test/images/single-bundle-index
docker push quay.io/${QUAY_REPO}/${BUNDLE_NAME}:${BUNDLE_TAG}

opm index add --bundles quay.io/${QUAY_REPO}/${BUNDLE_NAME}:${BUNDLE_TAG} --tag quay.io/${QUAY_REPO}/${INDEX_NAME}:${INDEX_TAG} -c docker
docker push quay.io/${QUAY_REPO}/${INDEX_NAME}:${INDEX_TAG}
