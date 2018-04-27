#!/usr/bin/env bash

./scripts/build_catalog_configmap.sh catalog_resources/ocs tectonic-ocs deploy/chart/templates/08-tectonicocs.configmap.yaml
./scripts/build_catalog_configmap.sh catalog_resources/components tectonic-components deploy/chart/templates/09-tectoniccomponents.configmap.yaml
