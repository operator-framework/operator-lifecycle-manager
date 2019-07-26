## Background
OLM runs with `cluster-admin` privileges. By default, operator author can specify any set of permission(s) in the `CSV` and OLM will consequently grant it to the operator. In effect, an operator can achieve `cluster-scoped` privilege(s) which may not always be desired.

OLM introduced a new feature that applies principle of attenuation to the operator being installed. The administrator can specify a service account with a set of privilege(s) granted to it. OLM will ensure that when an operator is installed its privileges are confined to that of the service account specified.

As a result a `cluster-admin` can limit an Operator to a pre-defined set of RBAC rules. The Operator will not be able to do anything that is not explicitly permitted by those. This enables self-sufficient installation of Operators by non-`cluster-admin` users with a limited scope.

The section below describes how an administrator can achieve this.

### Scoped Operator Install
We will cover the following scenario - an administrator wants to confine a set of operator(s) to a designated namespace.

Execute the following steps to achieve this.

* Create a new namespace.
```bash
cat <<EOF | kubectl create -f -
apiVersion: v1
kind: Namespace
metadata:
  name: scoped
EOF
```

* Allocate permission(s) that you want the operator(s) to be confined to. This involves creating a new `ServiceAccount`, relevant `Role(s)` and `RoleBinding(s)`.

```bash
cat <<EOF | kubectl create -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: scoped
  namespace: scoped
EOF
```

Since this is an exercise I am granting the `ServiceAccount` permission(s) to do anything in the designated namespace for simplicity. In real life we would create a more fine grained set of permission(s).

```bash
cat <<EOF | kubectl create -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: scoped
  namespace: scoped
rules:
- apiGroups: ["*"]
  resources: ["*"]
  verbs: ["*"]  
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: scoped-bindings
  namespace: scoped
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: scoped
subjects:
- kind: ServiceAccount
  name: scoped
  namespace: scoped
EOF
```

* Create an `OperatorGroup` in the designated namespace. This operator group targets the designated namespace to ensure that its tenancy is confined to it. In addition, `OperatorGroup` allows a user to specify a `ServiceAccount`. We will specify the `ServiceAccount` we created above. 

```bash
cat <<EOF | kubectl create -f -
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: scoped
  namespace: scoped
spec:
  serviceAccountName: scoped
  targetNamespaces:
  - scoped
EOF
```

Any operator being installed in the designated namespace will now be tied to this `OperatorGroup` and hence to the `ServiceAccount` specified. 

* Create a subscription in the designated namespace to install an operator. You can specify a `CatalogSource` that already exists in the designated namespace, or one that is in the global catalog namespace.
```bash
cat <<EOF | kubectl create -f -
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: etcd
  namespace: scoped
spec:
  channel: singlenamespace-alpha
  name: etcd
  source: operatorhubio-catalog
EOF
```
Any operator tied to this `OperatorGroup` will now be confined to the permission(s) granted to the specified `ServiceAccount`. If the operator asks for permission(s) that are outside the scope of the `ServiceAccount` the install will fail with appropriate error(s). When OLM installs the operator the following will happen:
* The given `Subscription` object is picked up by OLM.
* OLM fetches the `OperatorGroup` tied to this subscription.
* OLM determines that the `OperatorGroup` has a `ServiceAccount` specified.
* OLM creates a `client` scoped to the `ServiceAccount` and uses the scoped client to install the operator. This ensures that any permission requested by the operator is always confined to that of the `ServiceAccount` in `OperatorGroup`.
* OLM creates a new `ServiceAccount` with the set of permission(s) specified in the `csv` and assigns it to the operator. The operator runs as the assigned `ServiceAccount`. 


## When Operator Install Fails
If the operator install fails due to lack of permission(s), follow the steps below to identify the error(s).
* Start with the `Subscription` object, its `status` has an object reference `installPlanRef` that points to the `InstallPlan` object that attempted to create the necessary `[Cluster]Role[Binding](s)` for the operator.
```
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: etcd
  namespace: scoped
status:
  installPlanRef:
    apiVersion: operators.coreos.com/v1alpha1
    kind: InstallPlan
    name: install-4plp8
    namespace: scoped
    resourceVersion: "117359"
    uid: 2c1df80e-afea-11e9-bce3-5254009c9c23
```

* Check the status of the `InstallPlan` object, it will depict the error OLM ran into. Here is an example:
```
apiVersion: operators.coreos.com/v1alpha1
kind: InstallPlan
status:
  conditions:
  - lastTransitionTime: "2019-07-26T21:13:10Z"
    lastUpdateTime: "2019-07-26T21:13:10Z"
    message: 'error creating clusterrole etcdoperator.v0.9.4-clusterwide-dsfx4: clusterroles.rbac.authorization.k8s.io
      is forbidden: User "system:serviceaccount:scoped:scoped" cannot create resource
      "clusterroles" in API group "rbac.authorization.k8s.io" at the cluster scope'
    reason: InstallComponentFailed
    status: "False"
    type: Installed
  phase: Failed
```
The error message will tell you:
* The type of resource it failed to create, including the API group of the resource ( In this case `clusterroles` in `rbac.authorization.k8s.io` group).
* The name of the resource.
* The type of error - `is forbidden` tells you that the user does not have enough permission to do the operation.
* The name of the user who attempted to create/update the resource, it will refer to the `ServiceAccount` we have specified in the `OperatorGroup`.
* The scope of the operation, `cluster scope` or not.

The user can add the missing permission to the `ServiceAccount` and then iterate. Unfortunately, OLM does not provide the complete list of error(s) on the first try. This feature will be added in the future.

## Fine Grained Permission(s)
OLM uses the `ServiceAccount` specified in `OperatorGroup` to create or update the following resource(s) related to the operator being installed.
* `ClusterServiceVersion`
* `Subscription`
* `Secret`
* `ServiceAccount`
* `Service`
* `ClusterRole` and `ClusterRoleBinding`
* `Role` and `RoleBinding`

In order to confine operator(s) to a designated namespace the administrator can start by granting the following permission(s) to the `ServiceAccount`.
```
kind: Role
rules:
- apiGroups: ["operators.coreos.com"]
  resources: ["subscriptions", "clusterserviceversions"]
  verbs: ["get", "create", "update", "patch"]
- apiGroups: [""]
  resources: ["services", "serviceaccounts"]
  verbs: ["get", "create", "update", "patch"]
- apiGroups: ["rbac.authorization.k8s.io"]
  resources: ["roles", "rolebindings"]
  verbs: ["get", "create", "update", "patch"]

  # Add permission(s) to create deployment/pods and other resource(s).
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["list", "watch", "get", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list", "watch", "get", "create", "update", "patch", "delete"]
```

If any operator specifies a pull secret then the following permission needs to be added.
```
# This is needed to get the secret from OLM namespace.
kind: ClusterRole
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get"]

---

kind: Role
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["create", "update", "patch"]
```