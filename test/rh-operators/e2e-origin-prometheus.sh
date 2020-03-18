#!/bin/bash

git clone https://github.com/openshift/origin.git -b master
cd origin

export GO111MODULE=off

# install Kubectl required by the test.
curl -LO https://storage.googleapis.com/kubernetes-release/release/v1.17.0/bin/linux/amd64/kubectl
chmod +x ./kubectl
mkdir -p /tmp/shared
export PATH=$PATH:/tmp/shared
mv ./kubectl /tmp/shared/kubectl

# make and run test
make WHAT=cmd/openshift-tests
DIR="./_output/local/bin/$(go env GOHOSTOS)/$(go env GOHOSTARCH)"

${DIR}/openshift-tests run all --dry-run | grep "\[sig-instrumentation\].*Prometheus\|\[sig-instrumentation\].*Alerts" | ${DIR}/openshift-tests run --junit-dir=./ -o ./junit.e2e.xml -f -
export JUNIT_REPORT_OUTPUT=$(pwd)/junit.e2e.xml
