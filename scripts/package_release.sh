#!/usr/bin/env bash

if [[ ${#@} -lt 3 ]]; then
    echo "Usage: $0 semver chart values"
    echo "* semver: semver-formatted version for this package"
    echo "* chart: the directory to output the chart"
    echo "* values: the values file"
    exit 1
fi

version=$1
chartdir=$2
values=$3

charttmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t charttmpdir)

charttmpdir=${charttmpdir}/chart

cp -R deploy/chart/ "${charttmpdir}"

# overwrite the destination Chart.yaml file with a modified copy containing the version
sed "s/^[Vv]ersion:.*\$/version: ${version}/" deploy/chart/Chart.yaml > "${charttmpdir}/Chart.yaml"

mkdir -p "${chartdir}"

go run -mod=vendor helm.sh/helm/v3/cmd/helm template -n olm -f "${values}" --include-crds --output-dir "${charttmpdir}" "${charttmpdir}"

cp -R "${charttmpdir}"/olm/{templates,crds}/. "${chartdir}"
