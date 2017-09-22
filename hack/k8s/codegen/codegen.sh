#!/usr/bin/env bash

# TODO: Once https://github.com/kubernetes/kubernetes/pull/52186 is merged,
#   we would use codegen.sh from code-generator repo.

function codegen::join() { local IFS="$1"; shift; echo "$*"; }

function codegen::generate-groups() {
  local GENS="$1" # the generators comma separated to run (deepcopy,defaulter,conversion,client,lister,informer) or "all"
  local OUTPUT_PKG="$2" # the output package name (e.g. github.com/example/project)
  local APIS_PKG="$3" # the external types dir (e.g. github.com/example/api or github.com/example/project/pkg/apis)
  local GROUPS_WITH_VERSIONS="$4" # groupA:v1,v2,groupB,v1,groupC:v2
  shift 4

  # enumerate group versions
  local FQ_APIS=() # e.g. k8s.io/api/apps/v1
  for GVs in ${GROUPS_WITH_VERSIONS}; do
    IFS=: read G Vs <<<"${GVs}"

    # enumerate versions
    for V in ${Vs//,/ }; do
      FQ_APIS+=(${APIS_PKG}/${G}/${V})
    done
  done

  if [ "${GENS}" = "all" ] || grep -qw "deepcopy" <<<"${GENS}"; then
    echo "Generating deepcopy funcs"
    ${GOPATH}/bin/deepcopy-gen -i $(codegen::join , "${FQ_APIS[@]}") -O zz_generated.deepcopy --bounding-dirs ${APIS_PKG} "$@"
  fi

  if [ "${GENS}" = "all" ] || grep -qw "client" <<<"${GENS}"; then
    echo "Generating clientset for ${GROUPS_WITH_VERSIONS} at ${OUTPUT_PKG}/clientset"
    ${GOPATH}/bin/client-gen --clientset-name versioned --input-base "" --input $(codegen::join , "${FQ_APIS[@]}") --clientset-path ${OUTPUT_PKG}/clientset "$@"
  fi

  if [ "${GENS}" = "all" ] || grep -qw "lister" <<<"${GENS}"; then
    echo "Generating listers for ${GROUPS_WITH_VERSIONS} at ${OUTPUT_PKG}/listers"
    ${GOPATH}/bin/lister-gen --input-dirs $(codegen::join , "${FQ_APIS[@]}") --output-package ${OUTPUT_PKG}/listers "$@"
  fi

  if [ "${GENS}" = "all" ] || grep -qw "informer" <<<"${GENS}"; then
    echo "Generating informers for ${GROUPS_WITH_VERSIONS} at ${OUTPUT_PKG}/informers"
    ${GOPATH}/bin/informer-gen \
             --input-dirs $(codegen::join , "${FQ_APIS[@]}") \
             --versioned-clientset-package ${OUTPUT_PKG}/clientset/versioned \
             --listers-package ${OUTPUT_PKG}/listers \
             --output-package ${OUTPUT_PKG}/informers \
             "$@"
  fi
}
