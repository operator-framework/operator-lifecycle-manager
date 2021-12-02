#! /bin/bash

set -o pipefail
set -o nounset
set -o errexit

: "${KUBECONFIG:?}"
: "${TEST_NAMESPACE:?}"
: "${TEST_ARTIFACTS_DIR:?}"

mkdir -p "${TEST_ARTIFACTS_DIR}"

commands=()
commands+=("get catalogsources -o yaml")
commands+=("get subscriptions -o yaml")
commands+=("get operatorgroups -o yaml")
commands+=("get clusterserviceversions -o yaml")
commands+=("get installplans -o yaml")
commands+=("get pods -o wide")
commands+=("get events --sort-by .lastTimestamp")

echo "Storing the test artifact output in the ${TEST_ARTIFACTS_DIR} directory"
for command in "${commands[@]}"; do
    echo "Collecting ${command} output..."
    COMMAND_OUTPUT_FILE=${TEST_ARTIFACTS_DIR}/${command// /_}
    kubectl -n ${TEST_NAMESPACE} ${command} >> "${COMMAND_OUTPUT_FILE}"
done
