#!/usr/bin/env bash

# requires helm to be installed

if [ ${#@} < 4 ]; then
    echo "Usage: $0 version alm-sha catalog-sha"
    echo "* semver: semver-formatted version for this package"
    echo "* chart: the directory to output the chart"
    echo "* values: the values file"
    exit 1
fi

version=$1
chartdir=$2
values=$3

charttmpdir=`mktemp -d 2>/dev/null || mktemp -d -t 'charttmpdir'`
mkdir ${charttmpdir}/chart
charttmpdir=${charttmpdir}/chart

cp -R deploy/chart/kube-1.8/ ${charttmpdir}/
echo "version: $1" >> ${charttmpdir}/Chart.yaml

mkdir ${chartdir}

pushd ${charttmpdir}/templates
filenames=$(ls *.yaml)
popd

for f in ${filenames}
do
  echo "Processing $f file..."
  helm template -n alm -f ${values} -x templates/${f} ${charttmpdir} > ${chartdir}/${f}
done
