#!/usr/bin/env bash

if [[ ${#@} < 3 ]]; then
    echo "Usage: $0 concatenate OLM's Kubernetes manifests into a single YAML stream and writes the result to a file"
    echo "* dir: the input directory that contains OLM's Kubernetes manifests"
    echo "* out: the output file for the combined OLM Kubernetes manifest"
    echo "* outcrds: the output file for the combined OLM CRD manifest"
    echo "* outscript: the output install script"
    exit 1
fi

manifestdir=$1
crddir=$2
out=$3
outcrds=$4
outscript=$5

rm -f ${out}
rm -f ${outcrds}
touch ${out}
touch ${outcrds}

for f in ${manifestdir}/*.yaml
do
    awk 'NR==1 && !/^---*/ {print "---"} !/^[[:space:]]*#/ {print}' $f >> ${out}
done

for f in ${crddir}/*.yaml
do 
    awk 'NR==1 && !/^---*/ {print "---"} !/^[[:space:]]*#/ {print}' $f >> ${outcrds}
done

echo "Wrote manifests to ${out} and ${outcrds}"

cp scripts/install.sh ${outscript}

