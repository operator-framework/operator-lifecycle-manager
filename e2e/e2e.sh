#!/usr/bin/env bash

# this script is run inside the container

mkdir /out
touch /out/test.log
/bin/e2e -test.v 2>&1 | tee /out/test.log | go tool test2json | jq 'if .Test !=null then . else empty end | if .Action == "fail" and .Test then "not ok # \(.Test)" elif .Action == "pass" and .Test then "ok # \(.Test)" elif .Action == "skip" and .Test then "ok # skip \(.Test)" else empty end'
