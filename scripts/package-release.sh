#!/usr/bin/env bash

# requires helm to be installed

if [[ ${#@} < 3 ]]; then
    echo "Usage: $0 semver chart values"
    echo "* semver: semver-formatted version for this package"
    echo "* chart: the directory to output the chart"
    echo "* values: the values file"
    exit 1
fi

version=$1
chartdir=$2
values=$3

charttmpdir=`mktemp -d 2>/dev/null || mktemp -d -t 'charttmpdir'`

charttmpdir=${charttmpdir}/chart

cp -R deploy/chart/ ${charttmpdir}
echo "version: $1" >> ${charttmpdir}/Chart.yaml

mkdir -p ${chartdir}

helm template -n olm -f ${values} ${charttmpdir} --output-dir ${charttmpdir}

cp -R ${charttmpdir}/olm/templates/. ${chartdir}

