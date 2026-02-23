## Requirement
Allow cluster administrator to specify a service account for an operator group so that all operator(s) associated with this operator group are deployed and run against the privileges granted to the service account.

`APIService` and `CustomResourceDefinition` will always be created  by `OLM` using the `cluster-admin` role. The service account(s) associated with operator group(s) should never be granted privileges to write these resources.

If the specified service account does not have enough permission(s) for an operator that is being installed, useful and contextual information should be added to the status of the respective resource(s) so that it is easy for the administrator to troubleshoot and resolve the issue.

## Scenarios:
* Administrator creates a new operator group and specifies a service account. All operator(s) associated with this operator group are installed and run against the privileges granted to the service account.

* Administrator creates a new operator group and does not specify any service account. We will  maintain backward compatibility. Same behavior as today.

* Existing operator group(s) (no service account is specified): We will maintain backward compatibility, same behavior as today.

* Administrator updates an existing operator group and specifies a service account. We can be permissive and allow the existing operator(s) to continue to run with their current privileges. When such an existing operator is going through an upgrade it should be reinstalled and run against the privileges granted to the service account like any new operator(s).

* The service account changes - permission may be added or taken away. Or existing service account is swapped with a new one.

* The administrator removes the service account from the operator group.

* The administrator has an untrusted operator and wants to run it with much less privileges than what the service account in the operator group allows.

## Scope
This feature will be implemented in phases. Phase 1 is scoped at:
* While creating permissions for an operator, use the service account specified in the operator group. This will ensure that the operator install will fail if it asks for a privilege not granted to the service account.
* The deployment of the operator(s) are carried out using the client bound to `cluster-admin` role granted to OLM. We are going to use a scoped client bound to the service account for deployment(s).

The following are not in scope for phase 1:
* We currently use `rbac authorizer` in `OLM` to check permission status. We are not introducing any change to `permissionStatus` function in this phase. In the future we can look into removing `rbac authorizer` from `OLM`. An alternate and more maintainable solution could be to use `SelfSubjectAccessReview` with a client bound to the service account of the operator.


## Proposed Changes
As part of the first phase, we propose the following changes:
* During reconciliation of `OperatorGroup` resource(s), if a service account is specified then:
  * Make sure the service account exists.
  * Update the Status of `OperatorGroup` with a reference to the `ServiceAccount`.

`OperatorGroupSpec` already has an attribute `ServiceAccount`. So the specification of `OperatorGroup` will not change. Also, we expect the `ServiceAccount` object to be in the same namespace as the `OperatorGroup`.

Add a new field in `OperatorGroupStatus` to refer to the resolved service account. 
```go
ServiceAccountRef *corev1.ObjectReference `json:"serviceAccountRef,omitempty"`
```

* Add ability to create a client that is bound to the bearer token of the service account specified in the operator group.

* While creating `(Cluster)Role`, `(Cluster)RoleBinding` object(s) for an operator being installed, use the client crafted above so that it is confined to the privileges granted to the service account specified in the operator group. `installPlanTransitioner.ExecutePlan` function is responsible for creating these role(s). Here is how we get access to the `OperatorGroup`:
```go
func (o *Operator) ExecutePlan(plan *v1alpha1.InstallPlan) error {
    ...
    // The operator group must be in the same namespace as the Installplan.
    // 1. List all OperatorGroup resource(s) in the same namespace as Installplan.
    list, err := lister.OperatorsV1().OperatorGroupLister().OperatorGroups(plan.GetNamespace()).List(labels.Everything())

    // Although we expect one OperatorGroup in a namespace, we should be defensive.
    // 2. Filter the list: 
    if len(Status.Namespaces) == 0 {
        // Remove from the list.
    }

    // If the resulting list has more than one OperatorGroup treat it as an error condition.
}
``` 

* The `InstallPlan` status will reflect the error(s) encountered if `OLM` fails to create the roles.

### How to build a client bound to a service account:
`InClusterConfig` attaches bearer token to to the`rest.Config` object returned. See https://github.com/kubernetes/client-go/blob/master/rest/config.go#L399. We can do the following to create a client that binds to a service account:
* Call `InClusterConfig` to create a `rest.Config` bound to the POD's `serviceaccount`.
* Use `AnonymousClientConfig` function to copy the `rest.Config` without the bearer token. https://github.com/kubernetes/client-go/blob/master/rest/config.go#L491
* Set `BearerToken` from the secret associated with the service account.