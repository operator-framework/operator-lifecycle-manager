#!/usr/bin/env bash

for crd in alphacatalogentry-v1s.app.coreos.com installplan-v1s.app.coreos.com clusterserviceversion-v1s.app.coreos.com
do
 for namespace in $(kubectl get $crd --no-headers=true --all-namespaces | awk '{ print $1 }' | sort | uniq);
 do
 kubectl --namespace $namespace delete $crd --all
 done
done

for crd in alertmanagers.monitoring.alm.coreos.com etcdclusters.etcd.database.coreos.com prometheuses.monitoring.alm.coreos.com servicemonitors.monitoring.alm.coreos.com vaultservices.vault.security.coreos.com
do
	kubectl delete customresourcedefinitions $crd
done