#!/usr/bin/env bash
# rebuild-example-operator-bundles.sh
#
# Rebuilds quay.io/olmtest/example-operator-bundle:{0.1.0,0.2.0,0.3.0} with
# gcr.io/kubebuilder/kube-rbac-proxy:v0.8.0 replaced by quay.io/brancz/kube-rbac-proxy:v0.8.0
# and tags the result as <TARGET_REGISTRY>/example-operator-bundle:<tag>.
#
# Usage:
#   ./hack/rebuild-example-operator-bundles.sh [TARGET_REGISTRY] [--push]
#
# Examples:
#   ./hack/rebuild-example-operator-bundles.sh quay.io/olmtest          # build only
#   ./hack/rebuild-example-operator-bundles.sh quay.io/olmtest --push   # build + push

set -euo pipefail

TARGET_REGISTRY="${1:-quay.io/olmtest}"
PUSH=false
if [[ "${2:-}" == "--push" ]]; then
  PUSH=true
fi

SOURCE_IMAGE="quay.io/olmtest/example-operator-bundle"
OLD_IMAGE="gcr.io/kubebuilder/kube-rbac-proxy:v0.8.0"
NEW_IMAGE="quay.io/brancz/kube-rbac-proxy:v0.8.0"
TAGS=(0.1.0 0.2.0 0.3.0)

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

for tag in "${TAGS[@]}"; do
  src="${SOURCE_IMAGE}:${tag}"
  dst="${TARGET_REGISTRY}/example-operator-bundle:${tag}"
  dir="${WORKDIR}/${tag}"
  mkdir -p "${dir}"

  echo "--- ${tag} ---"
  echo "[1/4] Pulling ${src}"
  docker pull -q "${src}"

  echo "[2/4] Extracting layers"
  docker save "${src}" | tar -C "${dir}" -xf -
  # Extract each layer into the work directory
  for layer in $(python3 -c "
import json
manifest = json.load(open('${dir}/manifest.json'))
for l in manifest[0]['Layers']:
    print(l)
"); do
    tar -xf "${dir}/${layer}" -C "${dir}" 2>/dev/null || true
  done

  echo "[3/4] Patching kube-rbac-proxy image reference"
  csv="${dir}/manifests/example-operator.clusterserviceversion.yaml"
  if grep -q "${OLD_IMAGE}" "${csv}"; then
    sed -i "s|${OLD_IMAGE}|${NEW_IMAGE}|g" "${csv}"
    echo "      ${OLD_IMAGE} -> ${NEW_IMAGE}"
  else
    echo "      (already using ${NEW_IMAGE} or not present)"
  fi

  echo "[4/4] Building ${dst}"
  # Reconstruct labels from the original image
  labels=$(docker inspect "${src}" | python3 -c "
import json, sys
d = json.load(sys.stdin)[0]
for k, v in d.get('Config', {}).get('Labels', {}).items():
    print(f'LABEL {k}={v}')
")
  dockerfile="${dir}/Dockerfile"
  cat > "${dockerfile}" <<EOF
FROM scratch
COPY manifests/ /manifests/
COPY metadata/ /metadata/
${labels}
EOF
  docker build -q -t "${dst}" "${dir}" -f "${dockerfile}"
  echo "      Built ${dst}"

  if [[ "${PUSH}" == "true" ]]; then
    echo "[push] Pushing ${dst}"
    docker push "${dst}"
  fi

  echo ""
done

echo "Done. Images built:"
for tag in "${TAGS[@]}"; do
  echo "  ${TARGET_REGISTRY}/example-operator-bundle:${tag}"
done

if [[ "${PUSH}" == "false" ]]; then
  echo ""
  echo "To push, rerun with --push:"
  echo "  $0 ${TARGET_REGISTRY} --push"
fi
