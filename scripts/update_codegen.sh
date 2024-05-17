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

# This script is based off: https://github.com/kubernetes/code-generator/blob/v0.30.0/examples/hack/update-codegen.sh
# It is used to update the generated code for the OLM API and package-server API.

set -o errexit
set -o nounset
set -o pipefail

# Setting the SCRIPT_ROOT and attempting to locate the vendored code generator directory.
SCRIPT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
CODEGEN_PKG=$(cd "${SCRIPT_ROOT}" && ls -d ./vendor/k8s.io/code-generator 2>/dev/null)

# Check if the CODEGEN_PKG has been set and points to a directory, else throw an error.
if [[ -z "$CODEGEN_PKG" || ! -d "$CODEGEN_PKG" ]]; then
    echo "Error: Required vendored code generator directory does not exist." >&2
    exit 1
fi

# Set verbosity of code-generators
export KUBE_VERBOSE=2

# Set module and boilerplate paths
API_MODULE="github.com/operator-framework/api"
OLM_MODULE="github.com/operator-framework/operator-lifecycle-manager"
BOILERPLATE="$SCRIPT_ROOT/boilerplate.go.txt"

# Use vendored code generators
CLIENT_GEN="go run ${CODEGEN_PKG}/cmd/client-gen"
LISTER_GEN="go run ${CODEGEN_PKG}/cmd/lister-gen"
INFORMER_GEN="go run ${CODEGEN_PKG}/cmd/informer-gen"

# Source the kube_codegen.sh script to generate the OLM API client, listers and informers
source "${CODEGEN_PKG}/kube_codegen.sh"

##################################################
# Generate OLM API client, listers and informers #
##################################################
kube::codegen::gen_client \
  --with-watch \
  --output-dir "${SCRIPT_ROOT}/pkg/api/client" \
  --output-pkg "${OLM_MODULE}/pkg/api/client" \
  --boilerplate "${BOILERPLATE}" \
  "${SCRIPT_ROOT}/vendor/${API_MODULE}/pkg"

##############################################################
# Generate Package Manager API client, listers and informers #
##############################################################

# NOTE: The kube_codegen.sh script does not seem to support generating clients for the package-server internal API.
# Therefore, we will generate the clients for the package-server API manually.

# When generating the openapi, we can optionally update the known api violation report
# New violations will break the codegen process
REPORT_FILENAME="${SCRIPT_ROOT}/scripts/codegen_violation_exceptions.list"
if [[ "${UPDATE_API_KNOWN_VIOLATIONS:-}" == "true" ]]; then
  UPDATE_REPORT="--update-report"
fi

# generate openapi
kube::codegen::gen_openapi \
    --output-dir "${SCRIPT_ROOT}/pkg/package-server/client/openapi" \
    --output-pkg "${OLM_MODULE}/pkg/package-server/client/openapi" \
    --extra-pkgs "${API_MODULE}/pkg/operators/v1alpha1" \
    --extra-pkgs "${API_MODULE}/pkg/lib/version" \
    --boilerplate "${BOILERPLATE}" \
    --report-filename "${REPORT_FILENAME}" \
    ${UPDATE_REPORT:+"${UPDATE_REPORT}"} \
    "${SCRIPT_ROOT}/pkg/package-server/apis" # input

# generate clients
# generate pacakge-server operators/v1 client
${CLIENT_GEN} \
  -v "${KUBE_VERBOSE}" \
  --go-header-file "${BOILERPLATE}" \
  --output-dir "${SCRIPT_ROOT}/pkg/package-server/client/clientset" \
  --output-pkg "${OLM_MODULE}/pkg/package-server/client/clientset" \
  --clientset-name versioned \
  --input-base "${SCRIPT_ROOT}/pkg/package-server/apis" \
  --input operators/v1

# generate pacakge-server operators internal client
${CLIENT_GEN} \
  -v "${KUBE_VERBOSE}" \
  --go-header-file "${BOILERPLATE}" \
  --output-dir "${SCRIPT_ROOT}/pkg/package-server/client/clientset" \
  --output-pkg "${OLM_MODULE}/pkg/package-server/client/clientset" \
  --clientset-name internalversion \
  --input-base "${SCRIPT_ROOT}/pkg/package-server/apis" \
  --input operators

# generate listers for both api clients
${LISTER_GEN} \
  -v "${KUBE_VERBOSE}" \
  --go-header-file "${BOILERPLATE}" \
  --output-dir "${SCRIPT_ROOT}/pkg/package-server/client/listers" \
  --output-pkg "${OLM_MODULE}/pkg/package-server/client/listers" \
  "${OLM_MODULE}/pkg/package-server/apis/operators" \
  "${OLM_MODULE}/pkg/package-server/apis/operators/v1"

# generate informers for both api clients
${INFORMER_GEN} \
  -v "${KUBE_VERBOSE}" \
  --go-header-file "${BOILERPLATE}" \
  --output-dir "${SCRIPT_ROOT}/pkg/package-server/client/informers" \
  --output-pkg "${OLM_MODULE}/pkg/package-server/client/informers" \
  --versioned-clientset-package "${OLM_MODULE}/pkg/package-server/client/clientset/versioned" \
  --internal-clientset-package "${OLM_MODULE}/pkg/package-server/client/clientset/internalversion" \
  --listers-package "${OLM_MODULE}/pkg/package-server/client/listers" \
  "${OLM_MODULE}/pkg/package-server/apis/operators" \
  "${OLM_MODULE}/pkg/package-server/apis/operators/v1"
