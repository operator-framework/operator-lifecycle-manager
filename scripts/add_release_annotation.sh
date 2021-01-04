#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

chartdir=$1
yq=$2

for f in $chartdir/*.yaml; do
   $yq w -d'*' --inplace --style=double $f 'metadata.annotations['include.release.openshift.io/self-managed-high-availability']' true
done
