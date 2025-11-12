#!/usr/bin/env bash

# ==============================================================================
# Purpose: Generate minimal OpenShift CRDs for OpenShift controller unit tests
# ==============================================================================
#
# CONTEXT:
#   The OpenShift controller (pkg/controller/operators/openshift/) manages
#   ClusterOperator resources on OpenShift clusters to report OLM status.
#   
# PROBLEM:
#   - github.com/openshift/api v0.0.0-20251111193948+ removed individual CRD
#     YAML files from vendor (consolidated into a metadata-only manifest)
#   - Tests need actual CRD definitions to run in envtest environments
#   - Cannot fetch from upstream (files don't exist at that commit)
#
# SOLUTION:
#   Generate minimal CRDs with schemas matching what our tests require.
#   These are ONLY used for unit tests - production OCP clusters have the
#   actual CRDs provided by the platform.
#
# USED BY:
#   - ONLY pkg/controller/operators/openshift/suite_test.go (OpenShift Suite tests)
#   - NOT used by any other test suites or production code
#
# USAGE:
#   - Called by: make openshift-test-crds (part of make gen-all)
#   - Output: pkg/controller/operators/openshift/testdata/crds/*.yaml
#   - Loaded by: suite_test.go via envtest.Environment.CRDDirectoryPaths
# ==============================================================================

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
OUTPUT_DIR="${SCRIPT_ROOT}/pkg/controller/operators/openshift/testdata/crds"

# Ensure output directory exists
mkdir -p "${OUTPUT_DIR}"

echo "Generating minimal OpenShift CRDs for unit tests..."
echo "  Source: github.com/openshift/api (commit: 50e2ece149d7)"
echo "  Output: ${OUTPUT_DIR}"

cat > "${OUTPUT_DIR}/clusteroperators.config.openshift.io.yaml" <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusteroperators.config.openshift.io
spec:
  group: config.openshift.io
  names:
    kind: ClusterOperator
    listKind: ClusterOperatorList
    plural: clusteroperators
    shortNames:
    - co
    singular: clusteroperator
  scope: Cluster
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        description: ClusterOperator is the Custom Resource object which holds the current state of an operator
        properties:
          spec:
            description: spec holds configuration that could apply to any operator
            type: object
            x-kubernetes-preserve-unknown-fields: true
          status:
            description: status holds the information about the state of an operator
            properties:
              conditions:
                items:
                  properties:
                    lastTransitionTime:
                      format: date-time
                      type: string
                    message:
                      type: string
                    reason:
                      type: string
                    status:
                      type: string
                    type:
                      type: string
                  required:
                  - status
                  - type
                  type: object
                type: array
              relatedObjects:
                items:
                  properties:
                    group:
                      type: string
                    name:
                      type: string
                    namespace:
                      type: string
                    resource:
                      type: string
                  required:
                  - group
                  - name
                  - resource
                  type: object
                type: array
              versions:
                items:
                  properties:
                    name:
                      type: string
                    version:
                      type: string
                  required:
                  - name
                  - version
                  type: object
                type: array
            type: object
        type: object
    served: true
    storage: true
    subresources:
      status: {}
EOF

cat > "${OUTPUT_DIR}/clusterversions.config.openshift.io.yaml" <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusterversions.config.openshift.io
spec:
  group: config.openshift.io
  names:
    kind: ClusterVersion
    listKind: ClusterVersionList
    plural: clusterversions
    singular: clusterversion
  scope: Cluster
  versions:
  - name: v1
    schema:
      openAPIV3Schema:
        description: ClusterVersion is the configuration for the ClusterVersionOperator
        properties:
          spec:
            description: spec is the desired state
            type: object
            x-kubernetes-preserve-unknown-fields: true
          status:
            description: status contains information about the available updates
            type: object
            x-kubernetes-preserve-unknown-fields: true
        type: object
    served: true
    storage: true
    subresources:
      status: {}
EOF

echo ""
echo "Generated OpenShift CRDs for unit tests:"
for crd in "${OUTPUT_DIR}"/*.yaml; do
    echo "  - $(basename "$crd")"
done
echo ""
echo "These CRDs are loaded by pkg/controller/operators/openshift/suite_test.go"

