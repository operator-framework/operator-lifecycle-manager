#!/usr/bin/env bash

# this script is run inside the container
echo "$KUBECONFIG"
echo "$NAMESPACE"

mkdir /out
touch /out/test.log

# fail with the last non-zero exit code (preserves test fail exit code)
set -o pipefail

/bin/e2e -test.v 2>&1 | tee /out/test.log | go tool test2json | tee /out/test.json | jq -r -f /var/e2e/tap.jq

if cat /out/test.log | grep -q '^not'; then
  exit 1
else
  exit 0
fi
