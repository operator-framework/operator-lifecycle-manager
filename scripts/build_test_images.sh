#!/usr/bin/env bash

# Busybox Operator Index Image
docker build -t quay.io/olmtest/busybox-bundle:1.0.0 ./test/images/busybox-index/busybox/1.0.0
docker build -t quay.io/olmtest/busybox-bundle:2.0.0 ./test/images/busybox-index/busybox/2.0.0

docker build -t quay.io/olmtest/busybox-dependency-bundle:1.0.0 ./test/images/busybox-index/busybox-dependency/1.0.0
docker build -t quay.io/olmtest/busybox-dependency-bundle:2.0.0 ./test/images/busybox-index/busybox-dependency/2.0.0

docker push quay.io/olmtest/busybox-bundle:1.0.0
docker push quay.io/olmtest/busybox-bundle:2.0.0
docker push quay.io/olmtest/busybox-dependency-bundle:1.0.0
docker push quay.io/olmtest/busybox-dependency-bundle:2.0.0

opm index add --bundles quay.io/olmtest/busybox-dependency-bundle:1.0.0,quay.io/olmtest/busybox-bundle:1.0.0 --tag quay.io/olmtest/busybox-dependencies-index:1.0.0-with-ListBundles-method -c docker
docker push quay.io/olmtest/busybox-dependencies-index:1.0.0-with-ListBundles-method

opm index add --bundles quay.io/olmtest/busybox-dependency-bundle:2.0.0,quay.io/olmtest/busybox-bundle:2.0.0 --tag quay.io/olmtest/busybox-dependencies-index:2.0.0-with-ListBundles-method --from-index quay.io/olmtest/busybox-dependencies-index:1.0.0-with-ListBundles-method -c docker
docker push quay.io/olmtest/busybox-dependencies-index:2.0.0-with-ListBundles-method
