#!/bin/bash

git clone https://github.com/openshift/origin.git
cd origin

# mirrored from openshift/origin/test/extended/conformance-k8s.sh
source "$(dirname "${BASH_SOURCE}")/../../hack/lib/init.sh"

# Check inputs
if [[ -z "${KUBECONFIG-}" ]]; then
  os::log::fatal "KUBECONFIG must be set to a root account"
fi
test_report_dir="${ARTIFACT_DIR}"
mkdir -p "${test_report_dir}"

# TODO: get version from input arg
version="${KUBERNETES_VERSION:-release-1.17}"
kubernetes="${KUBERNETES_ROOT:-${OS_ROOT}/../../../k8s.io/kubernetes}"
if [[ -d "${kubernetes}" ]]; then
  git fetch origin --tags
else
  if [[ -n "${KUBERNETES_ROOT-}" ]]; then
    os::log::fatal "Cannot find Kubernetes source directory, set KUBERNETES_ROOT"
  fi
  kubernetes="${OS_ROOT}/_output/components/kubernetes"
  if [[ ! -d "${kubernetes}" ]]; then
    mkdir -p "$( dirname "${kubernetes}" )"
    os::log::info "Cloning Kubernetes source"
    git clone "https://github.com/kubernetes/kubernetes.git" -b "${version}" "${kubernetes}" # --depth=1 unfortunately we need history info as well
  fi
fi

os::log::info "Running Kubernetes conformance suite for ${version}"

# Execute OpenShift prerequisites
# Disable container security
oc adm policy add-scc-to-group privileged system:authenticated system:serviceaccounts
oc adm policy remove-scc-from-group restricted system:authenticated
oc adm policy remove-scc-from-group anyuid system:cluster-admins
# Mark the master nodes as unschedulable so tests ignore them
oc get nodes -o name -l 'node-role.kubernetes.io/master' | xargs -L1 oc adm cordon
unschedulable="$( ( oc get nodes -o name -l 'node-role.kubernetes.io/master'; ) | wc -l )"
# TODO: undo these operations

# Execute Kubernetes prerequisites
pushd "${kubernetes}" > /dev/null
git checkout "${version}"
make WHAT=cmd/kubectl
make WHAT=test/e2e/e2e.test
make WHAT=vendor/github.com/onsi/ginkgo/ginkgo
export PATH="${kubernetes}/_output/local/bin/$( os::build::host_platform ):${PATH}"

kubectl version  > "${test_report_dir}/version.txt"
echo "-----"    >> "${test_report_dir}/version.txt"
oc version      >> "${test_report_dir}/version.txt"

# Run the test, serial tests first, then parallel

rc=0

ginkgo \
  -nodes 1 -noColor '-focus=(\[sig-instrumentation\].*Prometheus|\[sig-instrumentation\].*Alerts)' $( which e2e.test ) -- \
  -report-dir "${test_report_dir}" \
  -allowed-not-ready-nodes ${unschedulable} \
  2>&1 | tee -a "${test_report_dir}/e2e.log" || rc=1

rename -v junit_ junit_serial_ ${test_report_dir}/junit*.xml

echo
echo "Run complete, results in ${test_report_dir}"

exit $rc
