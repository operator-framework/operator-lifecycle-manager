#!/usr/bin/env bash


TOOLS_BIN="${1?"Need to set full path to the location where binary tools will be downloaded"}"


# the username used to access registry.redhat.io (OPTIONAL)
RH_USER="${2:-}"
# the password used to access registry.redhat.io (OPTIONAL)
RH_PASSWORD="${3:-}"

mkdir -p "${TOOLS_BIN}"

# install upstream version of opm from github
function installUptreamOpm()
{
    local version=${1:-"v1.15.1"}
    local opmPrefix="opmup"
    local platform=""
    local architecture=""

    # translate OSTYPE to expected platform name
    if [[ "${OSTYPE}" == "linux-gnu"* ]]; then
        platform="linux"
    elif [[ "${OSTYPE}" == "darwin"* ]]; then
        platform="darwin"
    else
        # unsupported os
        return 1
    fi

    # translate HOSTTYPE to expected architecture name
    if [[ "${HOSTTYPE}" == "x86_64" ]]; then
        architecture="amd64"
    elif [[ "${HOSTTYPE}" == "s390x" ]]; then
        architecture="s390x"
    elif [[ "${HOSTTYPE}" == "powerpc64le" ]]; then
        architecture="ppc64le-opm"
    else
        # unsupported architecture
        return 1
    fi
    
    # download the upstream opm taking into account the version, platform, and architecture we're running on
    assetURL=$(curl -s "https://api.github.com/repos/operator-framework/operator-registry/releases/tags/${version}" | jq --argjson platform "\"${platform}\"" --argjson architecture "\"${architecture}\"" -r '.assets[] | select(.browser_download_url | contains( ($platform + "-" + $architecture) ) ) | .url')
    if ! "${TOOLS_BIN}/${opmPrefix}${version}" version 2>/dev/null && [[ -n ${assetURL} ]] ; then
        curl -sL -H "Accept: application/octet-stream" -o "${TOOLS_BIN}/${opmPrefix}${version}" "${assetURL}" && chmod 755 "${TOOLS_BIN}/${opmPrefix}${version}"
    fi

}

# installs downstream opm which requires docker for image extraction
function installDownstreamOpm() 
{
    local opmCatalogBinaryImage="${1:-"registry.redhat.io/openshift4/ose-operator-registry:v4.6.0"}"
    local version="${2:-"v1.14.3"}"
    local opmPrefix="opmdown"
    local executableName=""

    if [[ "${OSTYPE}" == "linux-gnu"* ]]; then
        executableName="opm"
    elif [[ "${OSTYPE}" == "darwin"* ]]; then
        executableName="darwin-amd64-opm"
    else
        # unsupported os
        return 1
    fi

    # check if we already have it
    if ! "${TOOLS_BIN}/${opmPrefix}${version}" version 2>/dev/null; then
        # docker login to redhat registry
        if echo "${RH_PASSWORD}" | docker login registry.redhat.io -u "\"${RH_USER}\"" --password-stdin &>/dev/null; then
            # create a offline container
            if containerReference=$(docker create "${opmCatalogBinaryImage}"); then
                # extract binary to tools bin and make it executable
                docker cp --follow-link "${containerReference}:/usr/bin/${executableName}" "${TOOLS_BIN}" && \
                    mv "${TOOLS_BIN}/${executableName}" "${TOOLS_BIN}/${opmPrefix}${version}" && \
                    chmod 755 "${TOOLS_BIN}/${opmPrefix}${version}"
            else
                # can't create offline container
                return 1
            fi
        else
            # can't login
            return 1
        fi
    fi
}

# installs the "oc" executable
function installOC() 
{
    local version=${1:-"4.5.0"}
    local tarfile="oc.tar.gz"
    local platform=""
    local linux_wildcard_flag=""
    local stripcomp=1
    local ocprefix="ocv"

    # linux tar requires a wildcard flag, but darwin does not
    # also set platform specific URL 
    if [[ "${OSTYPE}" == "linux-gnu"* ]]; then
        linux_wildcard_flag="yes"
        platform="linux"
    elif [[ "${OSTYPE}" == "darwin"* ]]; then
        linux_wildcard_flag=""
        platform="mac"
    else
        # unsupported os
        return 1
    fi
    # check if we already have it
    if ! "${TOOLS_BIN}/${ocprefix}${version}" version 2>/dev/null; then
        # download specific tar for version and platform, and make sure the download succeeds
        if curl -sL -H "Accept: application/octet-stream" "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${version}/openshift-client-${platform}-${version}.tar.gz" -o "${tarfile}" && [[ -f "${tarfile}" ]]; then
            # figure out if we need to strip off leading path elements
            [[ $(tar tvf ${tarfile} | grep -E -v kubectl | grep -E '/oc$| oc$' | rev | cut -f1 -d' ' | rev) =~ ^oc$ ]] && stripcomp=0 || stripcomp=1
            # extract oc executable, move it to tools bin with different name and cleanup tarfile
            tar xvf ${tarfile} --strip-components=$stripcomp --directory "${TOOLS_BIN}" ${linux_wildcard_flag:+'--wildcards'} '*oc' && \
                mv "${TOOLS_BIN}/oc" "${TOOLS_BIN}/${ocprefix}${version}" && \
                rm "${tarfile}"
        else
            return 1
        fi
    fi
}

# install all tools and bail at first sign of trouble
installOC "4.5.0" || exit $?
installUptreamOpm "v1.15.1" || exit $?
installUptreamOpm "v1.15.2" || exit $?

# optionally install downstream if creds provided
if [[ -n "${RH_PASSWORD}" ]] && [[ -n "${RH_USER}" ]] ; then
    installDownstreamOpm "registry.redhat.io/openshift4/ose-operator-registry:v4.6.0" "v1.14.3" || exit $?
fi