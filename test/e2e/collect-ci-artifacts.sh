#! /bin/bash

set -o pipefail
set -o nounset
set -o errexit

: "${KUBECONFIG:?}"
: "${TEST_NAMESPACE:?}"
: "${TEST_ARTIFACTS_DIR:?}"
: "${KUBECTL:=kubectl}"

echo "Using the ${KUBECTL} kubectl binary"
echo "Using the ${TEST_ARTIFACTS_DIR} output directory"
mkdir -p "${TEST_ARTIFACTS_DIR}"

OLM_RESOURCES="csv,sub,catsrc,installplan,og"

commands=()
commands+=("describe all,${OLM_RESOURCES}")
commands+=("get all,${OLM_RESOURCES} -o yaml")
commands+=("get all,${OLM_RESOURCES} -o wide")
commands+=("get events --sort-by .lastTimestamp")

echo "Storing the test artifact output in the ${TEST_ARTIFACTS_DIR} directory"
for command in "${commands[@]}"; do
    echo "Collecting ${command} output..."
    COMMAND_OUTPUT_FILE=${TEST_ARTIFACTS_DIR}/${command// /_}
    ${KUBECTL} -n "${TEST_NAMESPACE}" "${command}" >> "${COMMAND_OUTPUT_FILE}"
done

for pod in $(${KUBECTL} -n "${TEST_NAMESPACE}" get --no-headers pods | awk '{ print $1 }'); do
  echo "Collecting logs for pod: ${pod}"
  ${KUBECTL} -n "${TEST_NAMESPACE}" logs "${pod}" >> "${TEST_ARTIFACTS_DIR}/${pod}.log"
done
