#!/usr/bin/env bash
# update-test-catalogs.sh
#
# Regenerates the inline olm.bundle.object properties in the e2e test FBC
# catalog files.  This reproduces (and can refresh) the changes made in the
# commit that first introduced inline bundle objects.
#
# Subscription FBC files currently reference image: quay.io/olmtest/...
# Running this script converts them to inline objects (removing the image:
# field) and patches the kube-rbac-proxy image at the same time.
#
# The webhook catalog has no image: field; its existing blobs are patched
# in-place — no image pull required.
#
# Usage:
#   ./hack/update-test-catalogs.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_ROOT}"

BUNDLE_TO_FBC="go run ./hack/bundle-to-fbc/"

PROXY_08="gcr.io/kubebuilder/kube-rbac-proxy:v0.8.0=quay.io/brancz/kube-rbac-proxy:v0.8.0"
PROXY_05="gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0=quay.io/brancz/kube-rbac-proxy:v0.5.0"

# ---------------------------------------------------------------------------
# Subscription test catalogs
# Each file has an image: field pointing to the upstream olmtest bundle image.
# The script pulls that image, generates fresh olm.bundle.object properties,
# removes the image: field, and patches the proxy reference.
# ---------------------------------------------------------------------------

SUBSCRIPTION_DIR="test/e2e/testdata/subscription"

declare -A SUBSCRIPTION_BUNDLES=(
    ["example-operator.v0.1.0.yaml"]="quay.io/olmtest/example-operator-bundle:0.1.0"
    ["example-operator.v0.2.0.yaml"]="quay.io/olmtest/example-operator-bundle:0.2.0"
    ["example-operator.v0.2.0-deprecations.yaml"]="quay.io/olmtest/example-operator-bundle:0.2.0"
    ["example-operator.v0.3.0.yaml"]="quay.io/olmtest/example-operator-bundle:0.3.0"
    ["example-operator.v0.3.0-deprecations.yaml"]="quay.io/olmtest/example-operator-bundle:0.3.0"
)

echo "=== Subscription FBC files ==="
for file in "${!SUBSCRIPTION_BUNDLES[@]}"; do
    image="${SUBSCRIPTION_BUNDLES[$file]}"
    ${BUNDLE_TO_FBC} \
        --update "${SUBSCRIPTION_DIR}/${file}" \
        --image  "${image}" \
        --replace "${PROXY_08}"
done

# ---------------------------------------------------------------------------
# Webhook operator catalog
# This file was created by extracting the bundle from the webhook-operator
# index image (quay.io/operator-framework/webhook-operator-index:0.0.3).
# It has no image: field, so the script patches the existing blobs in-place —
# no image pull needed.
# ---------------------------------------------------------------------------

echo ""
echo "=== Webhook operator catalog ==="
${BUNDLE_TO_FBC} \
    --update "test/e2e/testdata/webhook/webhook-operator-catalog.yaml" \
    --replace "${PROXY_05}"

echo ""
echo "Done. Review the diffs with: git diff test/e2e/testdata/"
