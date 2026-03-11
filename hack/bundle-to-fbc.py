#!/usr/bin/env python3
"""
bundle-to-fbc.py — Generate or refresh inline olm.bundle.object properties in FBC files.

Two modes:

  PRINT mode (default): pull a bundle image, encode its manifests as
  olm.bundle.object properties, and print them to stdout.

    python3 hack/bundle-to-fbc.py <bundle-image> [--replace OLD=NEW ...]

  UPDATE mode: update an FBC file in-place.  Behaviour depends on whether the
  olm.bundle stanza already has an image: field:

    • Has image: field  → pull that bundle image (or --image if overriding),
                          generate fresh olm.bundle.object properties, remove
                          the image: field, and write the file.

    • No image: field   → decode the existing olm.bundle.object blobs, apply
                          --replace substitutions, re-encode, and write the
                          file.  No network access required.  Formatting is
                          preserved via text replacement (no YAML round-trip).

    python3 hack/bundle-to-fbc.py --update <fbc-file> [--image <bundle-image>]
                                   [--replace OLD=NEW ...]

Examples:

  # Print properties from a bundle image
  python3 hack/bundle-to-fbc.py quay.io/olmtest/example-operator-bundle:0.1.0

  # Convert a subscription FBC file from image: to inline objects
  python3 hack/bundle-to-fbc.py --update test/e2e/testdata/subscription/example-operator.v0.1.0.yaml \\
      --image quay.io/olmtest/example-operator-bundle:0.1.0 \\
      --replace gcr.io/kubebuilder/kube-rbac-proxy:v0.8.0=quay.io/brancz/kube-rbac-proxy:v0.8.0

  # Patch image references in an already-inline catalog without re-pulling anything
  python3 hack/bundle-to-fbc.py --update test/e2e/testdata/webhook/webhook-operator-catalog.yaml \\
      --replace gcr.io/kubebuilder/kube-rbac-proxy:v0.5.0=quay.io/brancz/kube-rbac-proxy:v0.5.0
"""

import argparse
import base64
import json
import os
import subprocess
import sys
import tarfile
import tempfile

try:
    import yaml
except ImportError:
    sys.exit("PyYAML is required: pip install pyyaml")


# ---------------------------------------------------------------------------
# Custom YAML types for controlled serialization
# ---------------------------------------------------------------------------

class FlowStyleDict(dict):
    """A dict subclass that PyYAML serializes in flow style: {key: value}."""
    pass


class IndentedDumper(yaml.Dumper):
    """Dumper that indents sequence items (non-indentless), matching common FBC style."""

    def increase_indent(self, flow=False, indentless=False):
        return super().increase_indent(flow, False)


def _flow_dict_representer(dumper, data):
    return dumper.represent_mapping("tag:yaml.org,2002:map", data.items(), flow_style=True)


IndentedDumper.add_representer(FlowStyleDict, _flow_dict_representer)


# ---------------------------------------------------------------------------
# Image extraction helpers
# ---------------------------------------------------------------------------

def pull_and_extract(image: str, dest: str):
    """Pull a bundle image and extract all layer contents into dest."""
    print(f"[bundle-to-fbc] Pulling {image}", file=sys.stderr)
    subprocess.run(["docker", "pull", "-q", image], check=True, stdout=subprocess.DEVNULL)

    save_tar = os.path.join(dest, "image.tar")
    with open(save_tar, "wb") as f:
        subprocess.run(["docker", "save", image], check=True, stdout=f)

    with tarfile.open(save_tar) as t:
        t.extractall(dest, filter="data")

    with open(os.path.join(dest, "manifest.json")) as f:
        manifest = json.load(f)

    for layer_path in manifest[0]["Layers"]:
        with tarfile.open(os.path.join(dest, layer_path)) as t:
            t.extractall(dest, filter="data")


def manifest_files(bundle_dir: str):
    """Yield sorted paths to manifest YAML/JSON files inside manifests/."""
    manifests_dir = os.path.join(bundle_dir, "manifests")
    if not os.path.isdir(manifests_dir):
        sys.exit(f"No manifests/ directory found under {bundle_dir}")
    for name in sorted(os.listdir(manifests_dir)):
        if name.endswith((".yaml", ".yml", ".json")):
            yield os.path.join(manifests_dir, name)


# ---------------------------------------------------------------------------
# Encoding / decoding
# ---------------------------------------------------------------------------

def apply_replacements(text: str, replacements: dict) -> str:
    for old, new in replacements.items():
        text = text.replace(old, new)
    return text


def encode_manifest_file(path: str, replacements: dict) -> dict:
    """Read a manifest YAML/JSON file, patch it, compact-JSON-encode it, base64 it."""
    with open(path) as f:
        raw = apply_replacements(f.read(), replacements)
    obj = yaml.safe_load(raw)
    compact = json.dumps(obj, separators=(",", ":"))
    data = base64.b64encode(compact.encode()).decode()
    return {"type": "olm.bundle.object", "value": FlowStyleDict({"data": data})}


def patch_existing_blob(prop: dict, replacements: dict) -> dict:
    """Decode an existing olm.bundle.object blob, apply replacements, re-encode."""
    raw_json = base64.b64decode(prop["value"]["data"]).decode()
    patched = apply_replacements(raw_json, replacements)
    # Round-trip through JSON to normalise (parse then compact-dump)
    compact = json.dumps(json.loads(patched), separators=(",", ":"))
    data = base64.b64encode(compact.encode()).decode()
    return {"type": "olm.bundle.object", "value": FlowStyleDict({"data": data})}


def props_from_image(image: str, replacements: dict) -> list:
    """Pull a bundle image and return a list of olm.bundle.object property dicts."""
    with tempfile.TemporaryDirectory(prefix="bundle-to-fbc-") as tmpdir:
        pull_and_extract(image, tmpdir)
        props = []
        for path in manifest_files(tmpdir):
            with open(path) as f:
                kind = (yaml.safe_load(f) or {}).get("kind", os.path.basename(path))
            print(f"[bundle-to-fbc]   encoding {os.path.basename(path)} ({kind})", file=sys.stderr)
            props.append(encode_manifest_file(path, replacements))
    return props


# ---------------------------------------------------------------------------
# FBC file rewriting — two strategies
# ---------------------------------------------------------------------------

def _patch_in_place(fbc_path: str, replacements: dict, docs: list):
    """
    Patch existing olm.bundle.object blobs using text replacement only.

    This preserves all YAML formatting (flow-style values, list indentation,
    document separators) by swapping only the base64 data strings in the
    original file text.
    """
    with open(fbc_path) as f:
        content = f.read()

    blob_map = {}  # old_base64 → new_base64
    total = 0

    for doc in docs:
        if doc is None or doc.get("schema") != "olm.bundle":
            continue
        bundle_name = doc.get("name", "?")
        props = doc.get("properties", [])
        count = sum(1 for p in props if p.get("type") == "olm.bundle.object")
        if not count:
            print(
                f"[bundle-to-fbc] WARNING: bundle '{bundle_name}' has no image: "
                f"and no olm.bundle.object properties — nothing to update",
                file=sys.stderr,
            )
            continue
        print(
            f"[bundle-to-fbc] {fbc_path}: patching {count} existing blob(s) "
            f"for bundle '{bundle_name}'",
            file=sys.stderr,
        )
        total += count
        for prop in props:
            if prop.get("type") != "olm.bundle.object":
                continue
            old_data = prop["value"]["data"]
            new_prop = patch_existing_blob(prop, replacements)
            new_data = new_prop["value"]["data"]
            if old_data != new_data:
                blob_map[old_data] = new_data

    if not blob_map:
        print(f"[bundle-to-fbc] {fbc_path}: {total} blob(s) — no changes needed", file=sys.stderr)
        return

    new_content = content
    for old, new in blob_map.items():
        new_content = new_content.replace(old, new)

    with open(fbc_path, "w") as f:
        f.write(new_content)

    print(
        f"[bundle-to-fbc] wrote {fbc_path} ({len(blob_map)}/{total} blob(s) patched)",
        file=sys.stderr,
    )


def _update_with_images(fbc_path: str, override_image: str | None, replacements: dict, docs: list):
    """
    Pull bundle images and replace olm.bundle stanzas with inline objects.

    Uses a full YAML round-trip with IndentedDumper so that new
    olm.bundle.object property values are serialized in flow style.
    """
    out_docs = []

    for doc in docs:
        if doc is None:
            continue
        if doc.get("schema") != "olm.bundle":
            out_docs.append(doc)
            continue

        image_field = doc.get("image")
        source_image = override_image or image_field

        print(
            f"[bundle-to-fbc] {fbc_path}: pulling {source_image} for bundle '{doc['name']}'",
            file=sys.stderr,
        )
        new_object_props = props_from_image(source_image, replacements)

        # Keep all non-olm.bundle.object properties, append the fresh ones.
        kept = [p for p in doc.get("properties", []) if p["type"] != "olm.bundle.object"]
        doc["properties"] = kept + new_object_props
        doc.pop("image", None)  # remove image: field — objects are now inline
        out_docs.append(doc)

    with open(fbc_path, "w") as f:
        yaml.dump_all(
            out_docs, f,
            Dumper=IndentedDumper,
            default_flow_style=False,
            allow_unicode=True,
            sort_keys=False,
            explicit_start=True,
        )

    print(f"[bundle-to-fbc] wrote {fbc_path}", file=sys.stderr)


def update_fbc_file(fbc_path: str, override_image: str | None, replacements: dict):
    """
    Read an FBC YAML file, update every olm.bundle stanza in-place, write it back.

    Dispatches to one of two strategies:

      • Any bundle has an image: field (or --image was given):
        Pull bundle image(s), generate inline olm.bundle.object properties,
        remove image: field.  Uses YAML round-trip (IndentedDumper).

      • No bundle has an image: field:
        Decode+patch+re-encode existing olm.bundle.object blobs.
        Uses text replacement to preserve all YAML formatting.
    """
    with open(fbc_path) as f:
        raw = f.read()

    docs = list(yaml.safe_load_all(raw))

    needs_image_pull = override_image or any(
        doc.get("image")
        for doc in docs
        if doc and doc.get("schema") == "olm.bundle"
    )

    if needs_image_pull:
        _update_with_images(fbc_path, override_image, replacements, docs)
    else:
        _patch_in_place(fbc_path, replacements, docs)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------

def parse_replacements(raw: list) -> dict:
    result = {}
    for r in raw:
        if "=" not in r:
            sys.exit(f"--replace value must be OLD=NEW, got: {r!r}")
        old, new = r.split("=", 1)
        result[old] = new
    return result


def main():
    parser = argparse.ArgumentParser(
        description="Generate or refresh inline olm.bundle.object properties in FBC files.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "bundle_image",
        nargs="?",
        metavar="bundle-image",
        help="Bundle image to pull (print mode only)",
    )
    parser.add_argument(
        "--update",
        metavar="FBC_FILE",
        help="Update an FBC file in-place instead of printing to stdout",
    )
    parser.add_argument(
        "--image",
        metavar="BUNDLE_IMAGE",
        help="Override/provide the source bundle image when using --update",
    )
    parser.add_argument(
        "--replace",
        metavar="OLD=NEW",
        action="append",
        default=[],
        help="String replacement applied inside manifest content before encoding (repeatable)",
    )
    args = parser.parse_args()

    replacements = parse_replacements(args.replace)

    if args.update:
        update_fbc_file(args.update, args.image, replacements)
    elif args.bundle_image:
        props = props_from_image(args.bundle_image, replacements)
        for p in props:
            print(f"  - type: {p['type']}")
            print(f"    value: {{\"data\": \"{p['value']['data']}\" }}")
    else:
        parser.error("provide a bundle-image (print mode) or --update <fbc-file> (update mode)")


if __name__ == "__main__":
    main()
