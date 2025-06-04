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

U_FLAG='false'
B_FLAG=''

usage() {
    cat <<EOF
Usage:
  $0 [-b <git-ref>] [-h] [-u]

Reports on golang mod file version updates, returns an error when a go.mod
file exceeds the root go.mod file (used as a threshold).

Options:
  -b <git-ref>  git reference (branch or SHA) to use as a baseline.
                Defaults to 'main'.
  -h            Help (this text).
  -u            Error on any update, even below the threshold.
EOF
}

while getopts 'b:hu' f; do
    case "${f}" in
        b) B_FLAG="${OPTARG}" ;;
        h) usage
           exit 0 ;;
        u) U_FLAG='true' ;;
        *) echo "Unknown flag ${f}"
           usage
           exit 1 ;;
    esac
done

BASE_REF=${B_FLAG:-main}
ROOT_GO_MOD="./go.mod"
GO_VER=$(sed -En 's/^go (.*)$/\1/p' "${ROOT_GO_MOD}")
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
OVERRIDE_LABEL="override-go-verdiff"

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
        # We NEED to report on changes in the root go.mod, regardless of the U_FLAG
        if [ "${f}" == "${ROOT_GO_MOD}" ]; then
            echo "${f}: ${v}: Updated ROOT golang version from ${old}"
            RETCODE=1
            continue
        fi
        if ${U_FLAG}; then
            echo "${f}: ${v}: Updated golang version from ${old}"
            RETCODE=1
        fi
    fi
done

for l in ${LABELS}; do
    if [ "$l" == "${OVERRIDE_LABEL}" ]; then
        if [ ${RETCODE} -eq 1 ]; then
            echo ""
            echo "Found ${OVERRIDE_LABEL} label, overriding failed results."
            RETCODE=0
        fi
    fi
done

if [ ${RETCODE} -eq 1 ]; then
    echo ""
    echo "This test result may be overridden by applying the (${OVERRIDE_LABEL}) label to this PR and re-running the CI job."
fi

exit ${RETCODE}
