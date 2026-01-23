#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

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

source .bingo/variables.env

OLM_RELEASE_IMG_REF=$(go run util/image-canonical-ref/main.go ${IMAGE_REPO}:${version})
OPM_IMAGE_REF=$(go run util/image-canonical-ref/main.go ${OPERATOR_REGISTRY_IMAGE})

echo "Using OPM image ${OPM_IMAGE_REF}"
echo "Using OLM image ${OLM_RELEASE_IMG_REF}"

$YQ -i '.olm.image.ref = "'"${OLM_RELEASE_IMG_REF}"'"' $values
$YQ -i '.catalog.image.ref = "'"${OLM_RELEASE_IMG_REF}"'"' $values
$YQ -i '.package.image.ref = "'"${OLM_RELEASE_IMG_REF}"'"' $values
$YQ -i '.catalog.opmImageArgs = "--opmImage='"${OPM_IMAGE_REF}"'"' $values

charttmpdir=$(mktemp -d 2>/dev/null || mktemp -d -t charttmpdir)

charttmpdir=${charttmpdir}/chart

cp -R deploy/chart/ "${charttmpdir}"

# overwrite the destination Chart.yaml file with a modified copy containing the version
sed "s/^[Vv]ersion:.*\$/version: ${version}/" deploy/chart/Chart.yaml > "${charttmpdir}/Chart.yaml"

mkdir -p "${chartdir}"

${HELM} template -n olm -f "${values}" --include-crds --output-dir "${charttmpdir}" "${charttmpdir}"

cp -R "${charttmpdir}"/olm/{templates,crds}/. "${chartdir}"
