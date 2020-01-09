# Package Manifests

The package server is a component of OLM that reads catalog data from CatalogSources and presents it for consumption as a kubernetes API.

## List PackageManifests

```sh
$ kubectl get packagemanifests
NAME                                CATALOG               AGE
aqua                                Community Operators   16m
t8c                                 Community Operators   16m
planetscale                         Community Operators   16m
cassandra-operator                  Community Operators   16m
ripsaw                              Community Operators   16m
```

The value in the `NAME` column is the `packageName` to be used in a Subscription.

## Filter PackageManifests

PackageManifests can be filtered by the labels on the ClusterServiceVersion in the head of the default channel:

```
$ kubectl get packagemanifests -l my=fun-operator
NAME                                CATALOG               AGE
aqua                                Community Operators   16m
```
