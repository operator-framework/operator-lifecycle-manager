# RBAC generation for Local Development

When running OLM locally, (i.e. with `make run-local`), OLM will generate an admission webhook that:

 - Inspects ClusterServiceVersions that are being created
 - Detects whether your user account has enough permission to create the RBAC required for the operator
 - If so, creates the necessary RBAC for your operator.
 
This is not yet available in non-local releases of OLM.

To test this out:

```sh
$ make run-local
$ kubectl -n operators create csv.yaml
$ kubectl -n operators get rolebindings
```

Depending on how you are running locally, you may need to grant your user more permission. For example:

```sh
$ kubectl create clusterrolebinding minikube-cluster-admin --clusterrole=cluster-admin --serviceaccount=minikube-user
```
