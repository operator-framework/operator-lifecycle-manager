#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

set -x
SCRIPT_ROOT=$(dirname ${BASH_SOURCE})/..
CODEGEN_VERSION=$(grep 'k8s.io/code-generator' go.sum | awk '{print $2}' | tail -1 | awk -F '/' '{print $1}')
CODEGEN_PKG=$(echo `go env GOPATH`"/pkg/mod/k8s.io/code-generator@${CODEGEN_VERSION}")

if [[ ! -d ${CODEGEN_PKG} ]]; then
  echo "${CODEGEN_PKG} is missing. Running 'go mod download'."
  go mod download
fi

echo ">> Using ${CODEGEN_PKG}"

# code-generator does work with go.mod but makes assumptions about
# the project living in `$GOPATH/src`. To work around this and support
# any location; create a temporary directory, use this as an output
# base, and copy everything back once generated.
TEMP_DIR=$(mktemp -d)
cleanup() {
    echo ">> Removing ${TEMP_DIR}"
    rm -rf ${TEMP_DIR}
}
trap "cleanup" EXIT SIGINT

echo ">> Temporary output directory ${TEMP_DIR}"

# Ensure we can execute.
chmod +x ${CODEGEN_PKG}/generate-groups.sh
chmod +x ${CODEGEN_PKG}/generate-internal-groups.sh

ORG="github.com/operator-framework"
API_MODULE="${ORG}/api"
MODULE="${ORG}/operator-lifecycle-manager"

# generate the code with:
# --output-base    because this script should also be able to run inside the vendor dir of
#                  k8s.io/kubernetes. The output-base is needed for the generators to output into the vendor dir
#                  instead of the $GOPATH directly. For normal projects this can be dropped.
${CODEGEN_PKG}/generate-groups.sh  "client,lister,informer" \
  "${MODULE}/pkg/api/client" \
  "${API_MODULE}/pkg" \
  "operators:v1alpha1,v1alpha2,v1,v2" \
  --output-base "${TEMP_DIR}" \
  --go-header-file "${SCRIPT_ROOT}/boilerplate.go.txt"

export OPENAPI_EXTRA_PACKAGES="${API_MODULE}/pkg/operators/v1alpha1,${API_MODULE}/pkg/lib/version"
${CODEGEN_PKG}/generate-internal-groups.sh all \
  "${MODULE}/pkg/package-server/client" \
  "${MODULE}/pkg/package-server/apis" \
  "${MODULE}/pkg/package-server/apis" \
  "operators:v1" \
  --output-base "${TEMP_DIR}" \
  --go-header-file "${SCRIPT_ROOT}/boilerplate.go.txt"

# copy the generated resources
cp -R "${TEMP_DIR}/${MODULE}/." "${SCRIPT_ROOT}"
