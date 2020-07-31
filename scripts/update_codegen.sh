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

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}"; ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo ../code-generator)}

# create a temporary directory to generate code in and ensure we clean it up on exit
OUTPUT_BASE=$(mktemp -d)
trap 'rm -rf "${OUTPUT_BASE}"' ERR EXIT

ORG="github.com/operator-framework"
API_MODULE="${ORG}/api"
MODULE="${ORG}/operator-lifecycle-manager"

# generate the code with:
# --output-base    because this script should also be able to run inside the vendor dir of
#                  k8s.io/kubernetes. The output-base is needed for the generators to output into the vendor dir
#                  instead of the $GOPATH directly. For normal projects this can be dropped.
bash "${CODEGEN_PKG}/generate-groups.sh" "client,lister,informer" \
  "${MODULE}/pkg/api/client" \
  "${API_MODULE}/pkg" \
  "operators:v1alpha1,v1alpha2,v1" \
  --output-base "${OUTPUT_BASE}" \
  --go-header-file "${SCRIPT_ROOT}/boilerplate.go.txt"

export OPENAPI_EXTRA_PACKAGES="${API_MODULE}/pkg/operators/v1alpha1,${API_MODULE}/pkg/lib/version"
bash "${CODEGEN_PKG}/generate-internal-groups.sh" all \
  "${MODULE}/pkg/package-server/client" \
  "${MODULE}/pkg/package-server/apis" \
  "${MODULE}/pkg/package-server/apis" \
  "operators:v1" \
  --output-base "${OUTPUT_BASE}" \
  --go-header-file "${SCRIPT_ROOT}/boilerplate.go.txt"

# copy the generated resources
cp -R "${OUTPUT_BASE}/${MODULE}/." "${SCRIPT_ROOT}"

