#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 3 ]]; then
  echo "usage: $0 <output> <package> <type>" >&2
  exit 2
fi

out="$1"
pkg_path="$2"
type_name="$3"

# Resolve the fully-qualified package path even when invoked from GOPATH layouts.
module_pkg=$(GO111MODULE="${GO111MODULE:-on}" GOWORK="${GOWORK:-off}" GOFLAGS="${GOFLAGS:-}" go list "$pkg_path")

GO111MODULE="${GO111MODULE:-on}" GOWORK="${GOWORK:-off}" GOFLAGS="${GOFLAGS:-}" \
  go run github.com/maxbrunsfeld/counterfeiter/v6 -o "$out" "${module_pkg}.${type_name}"
