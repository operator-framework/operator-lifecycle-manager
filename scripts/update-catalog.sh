#!/usr/bin/env bash

./scripts/build_catalog_configmap.sh deploy/chart/kube-1.7/templates/tectonicocs.configmap.yaml
./scripts/build_catalog_configmap.sh deploy/chart/kube-1.8/templates/08-tectonicocs.configmap.yaml
