#!/usr/bin/env bash

# requires helm to be installed

if [[ ${#@} < 3 ]]; then
    echo "Usage: $0 semver chart values"
    echo "* semver: semver-formatted version for this package"
    echo "* file: the output manifest file containing all chart manifests with YAML concatenated"
    echo "* values: the values file"
    exit 1
fi

version=$1
manifest=$2
values=$3

charttmpdir=`mktemp -d 2>/dev/null || mktemp -d -t 'charttmpdir'`

charttmpdir=${charttmpdir}/chart

cp -R deploy/chart/ ${charttmpdir}
echo "Version: $1" >> ${charttmpdir}/Chart.yaml

helm template -n olm -f ${values} ${charttmpdir} --output-dir ${charttmpdir}

awk 'BEGINFILE {print "---"} !/^[[:space:]]*#/{print}' ${charttmpdir}/olm/templates/*.yaml > ${manifest} && \
   echo "Wrote manifest to ${manifest}"