# Operator Multitenancy with OperatorGroups

An `OperatorGroup` is an OLM resource that provides rudimentary multitenant configuration to OLM installed operators.

## OperatorGroup Overview

* An `OperatorGroup` selects a set of target namespaces in which to generate required RBAC access for its member operators.
* The set of target namespaces is provided via a comma-delimited string stored in the `olm.targetNamespaces` annotation. This annotation is applied to member operator's `ClusterServiceVersion` (CSV) instances and is projected into their deployments. It is accessible to operator containers using [The Downward API](https://kubernetes.io/docs/tasks/inject-data-application/downward-api-volume-expose-pod-information/#the-downward-api)
* An operator is said to be a [member of an `OperatorGroup`](#operatorgroup-membership) if its CSV exists in the same namespace as the `OperatorGroup` and its CSV's [`InstallModes` support the set of namespaces targeted by the `OperatorGroup`](#installmodes-and-supported-operatorgroups)
* In order to transition, a CSV must be an active member of an `OperatorGroup` that has no [provided API conflicts with intersecting `OperatorGroups`](#operatorgroup-intersection)

## OperatorGroup Membership

An operator defined by CSV `csv-a` is said to be a _member_ of `OperatorGroup` `op-a` in namespace `ns-a` if both of the following hold:
* `op-a` is the only `OperatorGroup` in `ns-a`
* `csv-a`'s `InstallMode`s support `op-a`'s target namespace set

### TooManyOperatorGroups

If there exists more than one `OperatorGroup` in a single namespace, any CSV created in that namespace will transition to a failure state with reason `TooManyOperatorGroups`. CSVs in a failed state for this reason will transition to pending once the number of `OperatorGroup`s in their namespaces reaches one.

### InstallModes and Supported OperatorGroups

An `InstallMode` consists of an `InstallModeType` field and a boolean `Supported` field. A CSV's spec can contain a set of `InstallModes` of four distinct `InstallModeTypes`:
* `OwnNamespace`: If supported, the operator can be a member of an `OperatorGroup` that selects its own namespace
* `SingleNamespace`: If supported, the operator can be a member of an `OperatorGroup` that selects one namespace
* `MultiNamespace`: If supported, the operator can be a member of an `OperatorGroup` that selects more than one namespace
* `AllNamespaces`: If supported, the operator can be a member of an `OperatorGroup` that selects all namespaces (target namespace set is the empty string "")

> Note: If a CSV's spec omits an entry of `InstallModeType`, that type is considered unsupported unless support can be inferred by an existing entry that implicitly supports it.

### UnsupportedOperatorGroup

If a CSV's `InstallMode`s do not support the target namespace selection of the `OperatorGroup` in its namespace, the CSV will transition to a failure state with reason `UnsupportedOperatorGroup`. CSVs in a failed state for this reason will transition to pending once either the `OperatorGroups`'s target namespace selection changes to a supported configuration, or the CSV's `InstallMode`s are modified to support the `OperatorGroup`'s target namespace selection.

## Target Namespace Selection

Select the set of namespaces by specifying a label selector with the `spec.selector` field:

```yaml
apiVersion: operators.coreos.com/v1alpha2
kind: OperatorGroup
metadata:
  name: my-group
  namespace: my-namespace
spec:
  selector:
    matchLabels:
      cool.io/prod: "true"
```

or by explicitly naming target namespaces with the `spec.targetNamespaces` field:

```yaml
apiVersion: operators.coreos.com/v1alpha2
kind: OperatorGroup
metadata:
  name: my-group
  namespace: my-namespace
spec:
  targetNamespaces:
  - my-namespace
  - my-other-namespace
  - my-other-other-namespace
```

> Note: If both `spec.targetNamespaces` and `spec.selector` are defined, `spec.selector` is ignored.

Additionally, a _global_ `OperatorGroup` (which selects all namespaces) is specified by omitting both `spec.selector` and `spec.targetNamespaces`:

```yaml
apiVersion: operators.coreos.com/v1alpha2
kind: OperatorGroup
metadata:
  name: my-group
  namespace: my-namespace
```

The resolved set of selected namespaces is surfaced in an `OperatorGroup`'s `status.namespaces` field. A global `OperatorGroup`'s `status.namespace` is of length 1 and contains the empty string, `""`, which signals a consuming operator that it should watch all namespaces.

> Note: The consuming operator must know to treat `""` as an all namespace configuration.

## OperatorGroup CSV Annotations

Member CSVs of an `OperatorGroup` get the following annotations:
* `olm.operatorGroup=<group-name>`
  * Contains the name of the `OperatorGroup`
* `olm.operatorGroupNamespace=<group-namespace>`
  * Contains the namespace of the `OperatorGroup`
* `olm.targetNamespaces=<target-namespaces>`
  * Contains a comma-delimited string listing the `OperatorGroup`'s target namespace selection. This annotation is projected onto the pod template of a CSV's deployments and can be consumed by a pod instance via [The Downward API](https://kubernetes.io/docs/tasks/inject-data-application/downward-api-volume-expose-pod-information/#the-downward-api)

> Note: All annotations except `olm.targetNamespaces` are included with [copied CSVs](#copied-csvs). Omitting the `olm.targetNamespaces` annotation on copied CSVs prevents the names of target namespaces from being leaked between tenants.

## Provided APIs Annotation

Information about what `GroupVersionKinds`s (GVK) are provided by an `OperatorGroup` are surfaced in an `olm.providedAPIs` annotation. The annotation's value is a string consisting of a set of `<Kind>.<version>.<group>`s delimited with commas. The GVKs of CRDs and APIServices provided by all active member CSVs of an `OperatorGroup` are included.

Here's an example of an `OperatorGroup` with a single active member CSV providing the PackageManifests resource:

```yaml
apiVersion: operators.coreos.com/v1alpha2
kind: OperatorGroup
metadata:
  annotations:
    olm.providedAPIs: PackageManifest.v1alpha1.packages.apps.redhat.com
  name: olm-operators
  namespace: local
  ...
spec:
  selector: {}
  serviceAccount:
    metadata:
      creationTimestamp: null
  targetNamespaces:
  - local
status:
  lastUpdated: 2019-02-19T16:18:28Z
  namespaces:
  - local
```

## RBAC

When an `OperatorGroup` is created, 3 ClusterRoles each containing a single AggregationRule are generated:
* `<operatorgroup-name>-admin`
  * ClusterRoleSelector set to match the `olm.opgroup.permissions/aggregate-to-admin: <operatorgroup-name>` label

* `<operatorgroup-name>-edit`
  * ClusterRoleSelector set to match the `olm.opgroup.permissions/aggregate-to-edit: <operatorgroup-name>` label

* `<operatorgroup-name>-view`
  * ClusterRoleSelector set to match the `olm.opgroup.permissions/aggregate-to-view: <operatorgroup-name>` label

When a CSV becomes an active member of an `OperatorGroup` and is not in a failed state with reason InterOperatorGroupOwnerConflict, the following RBAC resources are generated:
* For each provided API resource from a CRD:
  * A `<kind.group-version-admin>` ClusterRole is generated with the `*` verb on `<group>` `<kind>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-admin: true` and `olm.opgroup.permissions/aggregate-to-admin: <operatorgroup-name>`
  * A `<kind.group-version-edit>` ClusterRole is generated with the `create, update, patch, delete` verbs on `<group>` `<kind>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-edit: true` and `olm.opgroup.permissions/aggregate-to-edit: <operatorgroup-name>`
  * A `<kind.group-version-view>` ClusterRole is generated with the `get, list, watch` verbs on `<group>` `<kind>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-view: true` and `olm.opgroup.permissions/aggregate-to-view: <operatorgroup-name>`
  * A `<kind.group-version-view-crd>` ClusterRole is generated with the `get` verb on `apiextensions.k8s.io` `customresourcedefinitions` `<crd-name>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-view: true` and `olm.opgroup.permissions/aggregate-to-view: <operatorgroup-name>`

* For each provided API resource from an APIService:
  * A `<kind.group-version-admin>` ClusterRole is generated with the `*` verb on `<group>` `<kind>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-admin: true` and `olm.opgroup.permissions/aggregate-to-admin: <operatorgroup-name>`
  * A `<kind.group-version-edit>` ClusterRole is generated with the `create, update, patch, delete` verbs on `<group>` `<kind>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-edit: true` and `olm.opgroup.permissions/aggregate-to-edit: <operatorgroup-name>`
  * A `<kind.group-version-view>` ClusterRole is generated with the `get, list, watch` verbs on `<group>` `<kind>` with aggregation labels `rbac.authorization.k8s.io/aggregate-to-view: true` and `olm.opgroup.permissions/aggregate-to-view: <operatorgroup-name>`

* For CSV in the _global_ `OperatorGroup`:
  * A ClusterRole and corresponding ClusterRoleBinding are generated for each permission defined in the CSV's permissions field. All resources generated are given the `olm.owner: <csv-name>` and `olm.owner.namespace: <csv-namespace>` labels
* Else for each target namespace:
  * All Roles and RoleBindings in the operator namespace with the `olm.owner: <csv-name>` and `olm.owner.namespace: <csv-namespace>` labels are copied into the target namespace.

## Copied CSVs

OLM will create copies of all active member CSVs of an `OperatorGroup` in each of that `OperatorGroup`'s target namespaces. The purpose of a Copied CSV is to tell users of a target namespace that a specific operator is configured to watch resources created there. Copied CSVs have a status reason _Copied_ and are updated to match the status of their source CSV. The `olm.targetNamespaces` annotation is stripped from copied CSVs before they are created on the cluster. Omitting the target namespace selection avoids an unnecessary information leak. Copied CSVs are deleted when their source CSV no longer exists or the operator group their source CSV belongs to no longer targets the copied CSV's namespace.

## Static OperatorGroups

An `OperatorGroup` is _static_ if it's `spec.staticProvidedAPIs` field is set to __true__. As a result, OLM does not modify the OperatorGroups's `olm.providedAPIs` annotation, which means that it can be set in advance. This is useful when a user wishes to use an `OperatorGroup` to prevent [resource contention](#what-can-go-wrong) in a set of namespaces, but does not have active member CSVs that provide the APIs for those resources.

Here's an example of an `OperatorGroup` that "protects" prometheus resources in all namespaces with the `something.cool.io/cluster-monitoring: "true"` annotation:

```yaml
apiVersion: operators.coreos.com/v1alpha2
kind: OperatorGroup
metadata:
  name: cluster-monitoring
  namespace: cluster-monitoring
  annotations:
    olm.providedAPIs: Alertmanager.v1.monitoring.coreos.com,Prometheus.v1.monitoring.coreos.com,PrometheusRule.v1.monitoring.coreos.com,ServiceMonitor.v1.monitoring.coreos.com
spec:
  staticProvidedAPIs: true
  selector:
    matchLabels:
      something.cool.io/cluster-monitoring: "true"
```

## OperatorGroup Intersection

### OperatorGroup Intersection Terminology

* Two `OperatorGroup`s are said to be _intersecting_ if the intersection of their target namespace sets __is not the empty set__
* Two `OperatorGroup`s are said to have _intersecting provided APIs_ if they are __intersecting__ and the intersection of their provided API sets (defined by `olm.providedAPIs` annotations) __is not the empty set__

### What Can Go Wrong?

`OperatorGroup`s with _intersecting provided APIs_ can compete for the same resources in the set of intersecting namespaces.

### Rules for Intersection

Each time an active member CSV syncs, OLM queries the cluster for the set of _intersecting provided APIs_ between the CSV's `OperatorGroup` and all other `OperatorGroup`s. OLM then checks if that set __is the empty set__:
* If __true__ and the CSV's provided APIs __are a subset__ of the `OperatorGroup`'s:
  * Continue transitioning
* If __true__ and the CSV's provided APIs __are not a subset__ of the `OperatorGroup`'s:
  * If the `OperatorGroup` [__is static__](#static-operatorgroups):
    * Clean up any deployments that belong to the CSV
    * Transition the CSV to a failed state with status reason CannotModifyStaticOperatorGroupProvidedAPIs
  * Else:
    * Replace the `OperatorGroup`'s `olm.providedAPIs` annotation with the union of itself and the CSV's provided APIs
* If __false__ and the CSV's provided APIs __are not a subset__ of the `OperatorGroup`'s:
  * Clean up any deployments that belong to the CSV
  * Transition the CSV to a failed state with status reason InterOperatorGroupOwnerConflict
* If __false__ and the CSV's provided APIs __are a subset__ of the `OperatorGroup`'s:
  * If the `OperatorGroup` [__is static__](#static-operatorgroups):
    * Clean up any deployments that belong to the CSV
    * Transition the CSV to a failed state with status reason CannotModifyStaticOperatorGroupProvidedAPIs
  * Else:
    * Replace the `OperatorGroup`'s `olm.providedAPIs` annotation with the difference between itself and the CSV's provided APIs

> Note: Failure states caused by `OperatorGroup`s are non-terminal.

> Note: When checking intersection rules, an `OperatorGroup`'s namespace is always included as part of its selected target namespaces.

Each time an `OperatorGroup` syncs:
* The set of provided APIs from active member CSV's is calculated from the cluster (ignoring [copied CSVs](#copied-csvs))
* The cluster set is compared to `olm.providedAPIs`:
  * If `olm.providedAPIs` contains any extraneous provided APIs:
    * `olm.providedAPIs` is pruned of any extraneous provided APIs (not provided on cluster)
* All CSVs that provide the same APIs across all namespaces (including those removed) are requeued. This notifies conflicting CSVs in intersecting groups that their conflict has possibly been resolved, either through resizing or through deletion of the conflicting CSV.
