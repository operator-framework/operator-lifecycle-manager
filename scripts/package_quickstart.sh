#!/usr/bin/env bash

if [[ ${#@} < 2 ]]; then
    echo "Usage: $0 concatenate OLM's Kubernetes manifests into a single YAML stream and writes the result to a file"
    echo "* dir: the input directory that contains OLM's Kubernetes manifests"
    echo "* out: the output file for the combined OLM Kubernetes manifest"
    exit 1
fi

dir=$1
out=$2

awk 'NR==1 && !/^---*/ {print "---"} !/^[[:space:]]*#/ {print}' ${dir}/*.yaml > ${out} && \
   echo "Wrote manifest to ${out}"

