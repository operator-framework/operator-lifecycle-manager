#! /bin/bash

OUTFILE=${1:-'catalog_resources/tectonicocs.configmap.yaml'}
NAMESPACE=${2:-'{{ .Values.catalog_namespace }}'}

cat <<EOF > $OUTFILE
kind: ConfigMap
apiVersion: v1
metadata:
  name: tectonic-ocs
  namespace: $NAMESPACE
  labels:
    tectonic-operators.coreos.com/managed-by: tectonic-x-operator
data:
  customResourceDefinitions: |-
EOF

for crd in catalog_resources/*.crd.yaml
do
  printf "    - " >> $OUTFILE
  head -n 1 $crd >> $OUTFILE
  tail -n +2 $crd | sed 's/^/      /' >> $OUTFILE
  sed -i -E 's/[[:space:]]*$//' $OUTFILE # trim trailing whitespace
done

printf '  clusterServiceVersions: |-\n' >> $OUTFILE

for csv in catalog_resources/*.clusterserviceversion.yaml
do
  printf "    - " >> $OUTFILE
  head -n 3 $csv | tail -n 1 >> $OUTFILE
  tail -n +4 $csv | sed 's/^/      /' >> $OUTFILE
  sed -i -E 's/[[:space:]]*$//' $OUTFILE # trim trailing whitespace
done
