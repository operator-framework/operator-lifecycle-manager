#!/usr/bin/env bash

DIR=${1:-'catalog_resources/ocs'}
NAME=${2:-'tectonic-ocs'}
OUTFILE=${3:-'catalog_resources/ocs/tectonicocs.configmap.yaml'}
NAMESPACE=${4:-'{{ .Values.catalog_namespace }}'}

cat <<EOF > $OUTFILE
{{- if ( has "$NAME" .Values.catalog_sources ) }}
kind: ConfigMap
apiVersion: v1
metadata:
  name: $NAME
  namespace: $NAMESPACE
  labels:
    tectonic-operators.coreos.com/managed-by: tectonic-x-operator
data:
  customResourceDefinitions: |-
EOF

for crd in $DIR/*.crd.yaml
do
  printf "    - " >> $OUTFILE
  head -n 1 $crd >> $OUTFILE
  tail -n +2 $crd | sed 's/^/      /' >> $OUTFILE
  # need -i.bak for mac/linux cross-compat
  sed -E -i.bak 's/[[:space:]]*$//' $OUTFILE # trim trailing whitespace
done

printf '  clusterServiceVersions: |-\n' >> $OUTFILE

for csv in $DIR/*.clusterserviceversion.yaml
do
  printf "    - " >> $OUTFILE
  head -n 3 $csv | tail -n 1 >> $OUTFILE
  # need -i.bak for mac/linux cross-compat
  tail -n +4 $csv | sed 's/^/      /' >> $OUTFILE
  sed -E -i.bak 's/[[:space:]]*$//' $OUTFILE # trim trailing whitespace
done

printf '  packages: |-\n' >> $OUTFILE

for csv in $DIR/*.package.yaml
do
  printf "    - " >> $OUTFILE
  head -n 2 $csv | tail -n 1 >> $OUTFILE
  # need -i.bak for mac/linux cross-compat
  tail -n +3 $csv | sed 's/^/      /' >> $OUTFILE
  sed -E -i.bak 's/[[:space:]]*$//' $OUTFILE # trim trailing whitespace
done

echo "{{ end }}" >> $OUTFILE
