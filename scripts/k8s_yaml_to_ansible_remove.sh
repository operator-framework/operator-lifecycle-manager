#!/usr/bin/env bash

# requires yq to be installed https://github.com/mikefarah/yq

if [[ ${#@} < 2 ]]; then
    echo "Usage: $0 manifests outfile"
    echo "* manifests: directory of k8s manifests"
    echo "* outfile: the ansible file to append"
    exit 1
fi

manifests=$1
outfile=$2

for filename in $manifests/*.yaml; do
  kind=$(yq r "$filename" kind)
  name=$(yq r "$filename" metadata.name)
  cat <<EOF >> $outfile

- name: Remove $name $kind manifest
  oc_obj:
    state: absent
    kind: $kind
    name: $name
    namespace: operator-lifecycle-manager
EOF
done
