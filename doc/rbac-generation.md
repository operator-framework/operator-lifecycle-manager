# RBAC generation for Operator Development

OLM ships with an Admission Webhook that:

 - Inspects ClusterServiceVersions that are being created
 - Detects whether your user account has enough permission to create the RBAC required for the operator
 - If so, creates the necessary RBAC for your operator.

To test this out locally:

```sh
$ make run-local
$ kubectl -n operators create csv.yaml
$ kubectl -n operators get rolebindings
```

Depending on how you are running locally, you may need to grant your user more permission. For example:

```sh
$ kubectl create clusterrolebinding minikube-cluster-admin --clusterrole=cluster-admin --user=minikube-user
```
