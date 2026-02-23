#!/usr/bin/env bash

function get_total_specs() {
  go run github.com/onsi/ginkgo/v2/ginkgo -noColor -dryRun -v -seed 1 "$@" ./test/e2e | grep -Po "Ran \K([0-9]+)(?= of .+ Specs in .+ seconds)"
}

unfocused_specs=$(get_total_specs)
label_filter=$(go run ./test/e2e/split/... -chunks 1 -print-chunk 0 ./test/e2e)
focused_specs=$(get_total_specs -label-filter "$label_filter")

if ! [ $unfocused_specs -eq $focused_specs ]; then
  echo "expected number of unfocused specs $unfocused_specs to equal focus specs $focused_specs"
  exit 1
fi
