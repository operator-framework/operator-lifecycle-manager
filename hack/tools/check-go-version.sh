#!/bin/bash

###########################################
# Check Go version in go.mod files
# and ensure it is not greater than the
# version in the main go.mod file.
# Also check if the version in the main
# go.mod file is updated in the
# submodules.
# This script is intended to be run
# as part of the CI pipeline to ensure
# that the version of Go that we can use
# is not accidentally upgraded.
# Source: https://github.com/operator-framework/operator-controller/blob/main/hack/tools/check-go-version.sh
#
# PS: We have the intention to centralize
# this implementation in the future.
###########################################


BASE_REF=${1:-main}
GO_VER=$(sed -En 's/^go (.*)$/\1/p' "go.mod")
OLDIFS="${IFS}"
IFS='.' MAX_VER=(${GO_VER})
IFS="${OLDIFS}"

if [ ${#MAX_VER[*]} -ne 3 -a ${#MAX_VER[*]} -ne 2 ]; then
    echo "Invalid go version: ${GO_VER}"
    exit 1
fi

GO_MAJOR=${MAX_VER[0]}
GO_MINOR=${MAX_VER[1]}
GO_PATCH=${MAX_VER[2]}

RETCODE=0

check_version () {
    local whole=$1
    local file=$2
    OLDIFS="${IFS}"
    IFS='.' ver=(${whole})
    IFS="${OLDIFS}"

    if [ ${ver[0]} -gt ${GO_MAJOR} ]; then
        echo "${file}: ${whole}: Bad golang version (expected ${GO_VER} or less)"
        return 1
    fi
    if [ ${ver[1]} -gt ${GO_MINOR} ]; then
        echo "${file}: ${whole}: Bad golang version (expected ${GO_VER} or less)"
        return 1
    fi

    if [ ${#ver[*]} -eq 2 ] ; then
        return 0
    fi
    if [ ${#ver[*]} -ne 3 ] ; then
        echo "${file}: ${whole}: Badly formatted golang version"
        return 1
    fi

    if [ ${ver[1]} -eq ${GO_MINOR} -a ${ver[2]} -gt ${GO_PATCH} ]; then
        echo "${file}: ${whole}: Bad golang version (expected ${GO_VER} or less)"
        return 1
    fi
    return 0
}

echo "Found golang version: ${GO_VER}"

for f in $(find . -name "*.mod"); do
    v=$(sed -En 's/^go (.*)$/\1/p' ${f})
    if [ -z ${v} ]; then
        echo "${f}: Skipping, no version found"
        continue
    fi
    if ! check_version ${v} ${f}; then
        RETCODE=1
    fi
    old=$(git grep -ohP '^go .*$' "${BASE_REF}" -- "${f}")
    old=${old#go }
    new=$(git grep -ohP '^go .*$' "${f}")
    new=${new#go }
    # If ${old} is empty, it means this is a new .mod file
    if [ -z "${old}" ]; then
        continue
    fi
    # Check if patch version remains 0: X.x.0 <-> X.x
    if [ "${new}.0" == "${old}" -o "${new}" == "${old}.0" ]; then
        continue
    fi
    if [ "${new}" != "${old}" ]; then
        echo "${f}: ${v}: Updated golang version from ${old}"
        RETCODE=1
    fi
done

exit ${RETCODE}
