#!/usr/bin/env bash
# rebuild-webhook-operator-index.sh
#
# Rebuilds quay.io/operator-framework/webhook-operator-index:0.0.3 with
# gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0 replaced by
# quay.io/brancz/kube-rbac-proxy:v0.5.0, and bundle objects embedded inline
# (removing the dependency on the original bundle image at runtime).
#
# Pulls both the original index image (for package/channel/bundle metadata)
# and the bundle image (quay.io/operator-framework/ci-bundle-webhook:0.0.3,
# for manifests), patches the proxy reference, and builds a fresh index image
# FROM opm.
#
# Usage:
#   ./hack/rebuild-webhook-operator-index.sh [TARGET_REGISTRY] [--push]
#
# Examples:
#   ./hack/rebuild-webhook-operator-index.sh quay.io/operator-framework          # build only
#   ./hack/rebuild-webhook-operator-index.sh quay.io/operator-framework --push   # build + push

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

TARGET_REGISTRY="${1:-quay.io/operator-framework}"
PUSH=false
if [[ "${2:-}" == "--push" ]]; then
  PUSH=true
fi

SOURCE_INDEX="quay.io/operator-framework/webhook-operator-index:0.0.3"
SOURCE_BUNDLE="quay.io/operator-framework/ci-bundle-webhook:0.0.3"
OLD_PROXY="gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0"
NEW_PROXY="quay.io/brancz/kube-rbac-proxy:v0.5.0"
OPM_IMAGE="quay.io/operator-framework/opm:v$(go list -m github.com/operator-framework/operator-registry | awk '{print $2}' | sed 's/^v//')"
DST="${TARGET_REGISTRY}/webhook-operator-index:0.0.3"

WORKDIR=$(mktemp -d)
trap 'rm -rf "${WORKDIR}"' EXIT

echo "[1/4] Pulling ${SOURCE_INDEX}"
docker pull -q "${SOURCE_INDEX}"

echo "[2/4] Pulling ${SOURCE_BUNDLE}"
docker pull -q "${SOURCE_BUNDLE}"

echo "[3/4] Building patched catalog"
mkdir -p "${WORKDIR}/catalog/webhook-operator"

python3 - <<EOF
import base64, json, os, tarfile, io
import subprocess

OLD_PROXY = "${OLD_PROXY}"
NEW_PROXY = "${NEW_PROXY}"
WORKDIR   = "${WORKDIR}"

def extract_image(image):
    """docker save an image and return a dict of path -> bytes for all files."""
    data = subprocess.check_output(["docker", "save", image])
    outer = tarfile.open(fileobj=io.BytesIO(data))
    files = {}
    for m in outer.getmembers():
        f = outer.extractfile(m)
        if f:
            files[m.name] = f.read()
    manifest = json.loads(files["manifest.json"])
    for layer_path in manifest[0]["Layers"]:
        layer_tar = tarfile.open(fileobj=io.BytesIO(files[layer_path]))
        for lm in layer_tar.getmembers():
            try:
                lf = layer_tar.extractfile(lm)
                if lf:
                    files[lm.name] = lf.read()
            except KeyError:
                pass  # skip symlinks to missing targets
    return files

# --- Extract package/channel/bundle skeleton from the index catalog.json ---
index_files = extract_image("${SOURCE_INDEX}")
catalog_json = index_files.get("catalog/webhook-operator/catalog.json", b"").decode()
# Parse multiple concatenated JSON objects (may be pretty-printed or one per line).
index_docs = []
decoder = json.JSONDecoder()
pos, text = 0, catalog_json.lstrip()
while text[pos:].strip():
    obj, idx = decoder.raw_decode(text, pos)
    index_docs.append(obj)
    pos = idx
    pos += len(text[pos:]) - len(text[pos:].lstrip())

# Collect non-blob properties from the bundle stanza; drop the image: field.
bundle_base_props = []
for doc in index_docs:
    if doc.get("schema") == "olm.bundle":
        bundle_base_props = [
            p for p in doc.get("properties", [])
            if p["type"] != "olm.bundle.object"
        ]
        break

# --- Extract manifests from the bundle image, patch, encode as inline objects ---
bundle_files = extract_image("${SOURCE_BUNDLE}")

bundle_object_props = []
manifest_names = sorted(
    name for name in bundle_files
    if name.startswith("manifests/") and
       any(name.endswith(ext) for ext in (".yaml", ".yml", ".json"))
)

for name in manifest_names:
    raw = bundle_files[name].decode()
    raw = raw.replace(OLD_PROXY, NEW_PROXY)
    # Parse YAML, re-encode as compact JSON (handles both YAML and JSON input).
    import yaml as _yaml
    obj = _yaml.safe_load(raw)
    compact = json.dumps(obj, separators=(",", ":"))
    compact = compact.replace(OLD_PROXY, NEW_PROXY)  # belt-and-suspenders
    data = base64.b64encode(compact.encode()).decode()
    kind = obj.get("kind", os.path.basename(name))
    print(f"  encoded {os.path.basename(name)} ({kind})")
    bundle_object_props.append({"type": "olm.bundle.object", "value": {"data": data}})

# --- Assemble the new catalog ---
out_docs = []
for doc in index_docs:
    if doc.get("schema") == "olm.bundle":
        doc = dict(doc)
        doc.pop("image", None)
        doc["properties"] = bundle_base_props + bundle_object_props
    out_docs.append(doc)

catalog_path = os.path.join(WORKDIR, "catalog", "webhook-operator", "catalog.json")
with open(catalog_path, "w") as f:
    for doc in out_docs:
        json.dump(doc, f)
        f.write("\n")

print(f"  wrote {catalog_path}")
EOF

echo "[4/4] Building ${DST}"
cat > "${WORKDIR}/Dockerfile" <<EOF
FROM ${OPM_IMAGE}
COPY catalog/ /catalog/
RUN ["/bin/opm", "serve", "/catalog", "--cache-dir=/tmp/cache", "--cache-only"]
EXPOSE 50051
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/catalog", "--cache-dir=/tmp/cache"]
EOF
docker build -q -t "${DST}" "${WORKDIR}" -f "${WORKDIR}/Dockerfile"
echo "      Built ${DST}"

if [[ "${PUSH}" == "true" ]]; then
  echo "[push] Pushing ${DST}"
  docker push "${DST}"
fi

echo ""
echo "Done. Image built: ${DST}"
if [[ "${PUSH}" == "false" ]]; then
  echo ""
  echo "To push, rerun with --push:"
  echo "  $0 ${TARGET_REGISTRY} --push"
fi
