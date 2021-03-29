#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

chartdir=$1
yq=$2

for f in $chartdir/*.yaml; do
   if [[ ! "$(basename "${f}")" =~ .*\.deployment\..* ]]; then
      $yq w -d'*' --inplace --style=double $f 'metadata.annotations['include.release.openshift.io/ibm-cloud-managed']' true
   else
      g="${f/%.yaml/.ibm-cloud-managed.yaml}"
      cp "${f}" "${g}"
      $yq w -d'*' --inplace --style=double $g 'metadata.annotations['include.release.openshift.io/ibm-cloud-managed']' true
      $yq d -d'*' --inplace $g 'spec.template.spec.nodeSelector."node-role.kubernetes.io/master"'
   fi
   $yq w -d'*' --inplace --style=double $f 'metadata.annotations['include.release.openshift.io/self-managed-high-availability']' true
   $yq w -d'*' --inplace --style=double $f 'metadata.annotations['include.release.openshift.io/single-node-developer']' true
done
