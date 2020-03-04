# Improved Subscription Status

Status: Pending

Version: Alpha

Implementation Owner: TBD

## Motivation

The `Subscription` `CustomResource` needs to expose useful information when a failure scenario is encountered. Failures can be encountered throughout a `Subscription`'s existence and can include issues with `InstallPlan` resolution, `CatalogSource` connectivity, `ClusterServiceVersion` (CSV) status, and more. To surface this information, explicit status for `Subscriptions` will be introduced via [status conditions](#status-conditions) which will be set by new, specialized status sync handlers for resources of interest (`Subscriptions`, `InstallPlan`s, `CatalogSource`s and CSVs).

### Following Conventions

In order to design a status that makes sense in the context of kubernetes resources, it's important to conform to current conventions. This will also help us avoid pitfalls that may have already been solved.

#### Status Conditions

The [kube api-conventions docs](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties) state that:
> Conditions should be added to explicitly convey properties that users and components care about rather than requiring those properties to be inferred from other observations.

A few internal Kubernetes resources that implement status conditions:

- [NodeStatus](https://github.com/kubernetes/kubernetes/blob/6c31101257bfcd47fa53702cea07fe2eedf2ad92/pkg/apis/core/types.go#L3556)
- [DeploymentStatus](https://github.com/kubernetes/kubernetes/blob/f5574bf62a051c4a41a3fff717cc0bad735827eb/pkg/apis/apps/types.go#L415)
- [DaemonSetStatus](https://github.com/kubernetes/kubernetes/blob/f5574bf62a051c4a41a3fff717cc0bad735827eb/pkg/apis/apps/types.go#L582)
- [ReplicaSetStatus](https://github.com/kubernetes/kubernetes/blob/f5574bf62a051c4a41a3fff717cc0bad735827eb/pkg/apis/apps/types.go#L751)

Introducing status conditions will let us have an explicit, level-based view of the current abnormal state of a `Subscription`. They are essentially orthogonal states (regions) of the compound state (`SubscriptionStatus`)¹. A conditionᵢ has a set of sub states [Unknown, True, False] each with sub states of their own [Reasonsᵢ],where Reasonsᵢ contains the set of transition reasons for conditionᵢ. This compound state can be used to inform a decision about performing an operation on the cluster.

> 1. [What is a statechart?](https://statecharts.github.io/what-is-a-statechart.html); see 'A state can have many "regions"'

#### References to Related Objects

The [kube api-convention docs](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#references-to-related-objects) state that:
> References to specific objects, especially specific resource versions and/or specific fields of those objects, are specified using the ObjectReference type (or other types representing strict subsets of it).

Rather than building our own abstractions to reference managed resources (like `InstallPlan`s), we can take advantage of the pre-existing `ObjectReference` type.

## Proposal

### Changes to SubscriptionStatus

- Introduce a `SubscriptionCondition` type
  - Describes a single state of a `Subscription` explicity
- Introduce a `SubscriptionConditionType` field
  - Describes the type of a condition
- Introduce a `Conditions` field of type `[]SubscriptionCondition` to `SubscriptionStatus`
  - Describes multiple potentially orthogonal states of a `Subscription` explicitly
- Introduce an `InstallPlanRef` field of type [*corev1.ObjectReference](https://github.com/kubernetes/kubernetes/blob/f5574bf62a051c4a41a3fff717cc0bad735827eb/pkg/apis/core/types.go#L3993)
  - To replace custom type with existing apimachinery type
- Deprecate the `Install` field
  - Value will be kept up to date to support older clients until a major version change
- Introduce a `SubscriptionCatalogStatus` type
  - Describes a Subscription's view of a CatalogSource's status
- Introduce a `CatalogStatus` field of type `[]SubscriptionCatalogStatus`
  - CatalogStatus contains the Subscription's view of its relevant CatalogSources' status

### Changes to Subscription Reconciliation

Changes to `Subscription` reconciliation can be broken into three parts:

1. Phase in use of `SubscriptionStatus.Install` with `SubscriptionStatus.InstallPlanRef`:
   - Write to `Install` and `InstallPlanRef` but still read from `Install`
   - Read from `InstallPlanRef`
   - Stop writing to `Install`
2. Create independent sync handlers and workqueues for resources of interest (status-handler) that only update specific `SubscriptionStatus` fields and `StatusConditions`:
   - Build actionable state reactively through objects of interest
   - Treat omitted `SubscriptionConditionTypes` in `SubscriptionStatus.Conditions` as having `ConditionStatus` "Unknown"
   - Add new status-handlers with new workqueues for:
     - `Subscription`s
     - `CatalogSource`s
     - `InstallPlan`s
     - CSVs
   - These sync handlers can be phased-in incrementally:
     - Add a conditions block and the `UpToDate` field, and ensure the `UpToDate` field is set properly when updating status
     - Pick one condition to start detecting, and write its status
     - Repeat with other conditions. This is a good opportunity to parallelize work immediate value to end-users (they start seeing the new conditions ASAP)
     - Once all conditions are being synchronized, start using them to set the state of other fields (e.g. `UpToDate`)
3. Add status-handler logic to toggle the `SubscriptionStatus.UpToDate` field:
   - Whenever `SubscriptionStatus.InstalledCSV == SubscriptionStatus.CurrentCSV` and `SubscriptionStatus.Conditions` has a `SubscriptionConditionType` of type `SubscriptionInstalledCSVReplacementAvailable` with `Status == "True"`, set `SubscriptionStatus.UpToDate = true`
   - Whenever `SubscriptionStatus.InstalledCSV != SubscriptionStatus.CurrentCSV`, set `SubscriptionStatus.UpToDate = false`

## Implementation

### SubscriptionStatus

Updated SusbcriptionStatus resource:

```go
import (
    // ...
    corev1 "k8s.io/kubernetes/pkg/apis/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    // ...
)

type SubscriptionStatus struct {
    // ObservedGeneration is the generation observed by the Subscription controller.
    // +optional
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`

    // CurrentCSV is the CSV the Subscription is progressing to.
    // +optional
    CurrentCSV   string                `json:"currentCSV,omitempty"`

    // InstalledCSV is the CSV currently installed by the Subscription.
    // +optional
    InstalledCSV string                `json:"installedCSV,omitempty"`

    // Install is a reference to the latest InstallPlan generated for the Subscription.
    // DEPRECATED: InstallPlanRef
    // +optional
    Install      *InstallPlanReference `json:"installplan,omitempty"`

    // State represents the current state of the Subscription
    // +optional
    State       SubscriptionState `json:"state,omitempty"`

    // Reason is the reason the Subscription was transitioned to its current state.
    // +optional
    Reason      ConditionReason   `json:"reason,omitempty"`

    // InstallPlanRef is a reference to the latest InstallPlan that contains the Subscription's current CSV.
    // +optional
    InstallPlanRef *corev1.ObjectReference `json:"installPlanRef,omitempty"`

    // CatalogStatus contains the Subscription's view of its relevant CatalogSources' status.
    // It is used to determine SubscriptionStatusConditions related to CatalogSources.
    // +optional
    CatalogStatus []SubscriptionCatalogStatus `json:"catalogStatus,omitempty"`

    // UpToDate is true when the latest CSV for the Subscription's package and channel is installed and running; false otherwise.
    //
    // This field is not a status SubscriptionCondition because it "represents a well-known state that applies to all instances of a kind"
    // (see https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties).
    // In this case, all Subscriptions are either up to date or not up to date.
    UpToDate bool `json:"UpToDate"`

    // LastUpdated represents the last time that the Subscription status was updated.
    LastUpdated metav1.Time       `json:"lastUpdated"`

    // Conditions is a list of the latest available observations about a Subscription's current state.
    // +optional
    Conditions []SubscriptionCondition  `json:"conditions,omitempty"`
}

// SubscriptionCatalogHealth describes a Subscription's view of a CatalogSource's status.
type SubscriptionCatalogStatus struct {
    // CatalogSourceRef is a reference to a CatalogSource.
    CatalogSourceRef *corev1.ObjectReference `json:"catalogSourceRef"`

    // LastUpdated represents the last time that the CatalogSourceHealth changed
    LastUpdated `json:"lastUpdated"`

    // Healthy is true if the CatalogSource is healthy; false otherwise.
    Healthy bool `json:"healthy"`
}

// SubscriptionConditionType indicates an explicit state condition about a Subscription in "abnormal-true"
// polarity form (see https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties).
type SusbcriptionConditionType string

const (
    // SubscriptionResolutionFails indicates the Subscription has failed to resolve a set
    SubscriptionResolutionFailed SubscriptionConditionType = "ResolutionFailed"

    // SubscriptionCatalogSourcesUnhealthy indicates that some or all of the CatalogSources to be used in resolution are unhealthy.
    SubscriptionCatalogSourcesUnhealthy SubscriptionConditionType = "CatalogSourcesUnhealthy"

    // SubscriptionCatalogSourceInvalid indicates the CatalogSource specified in the SubscriptionSpec is not valid.
    SubscriptionCatalogSourceInvalid SubscriptionConditionType = "CatalogSourceInvalid"

    // SubscriptionPackageChannelInvalid indicates the package and channel specified in the SubscriptionSpec is not valid.
    SubscriptionPackageChannelInvalid SubscriptionConditionType = "PackageChannelInvalid"

    // SubscriptionInstallPlanFailed indicates the InstallPlan responsible for installing the current CSV has failed.
    SubscriptionInstallPlanFailed SubscriptionConditionType = "InstallPlanFailed"

    // SubscriptionInstallPlanMissing indicates the InstallPlan responsible for installing the current CSV is missing.
    SubscriptionInstallPlanMissing SubscriptionConditionType = "InstallPlanMissing"

    // SubscriptionInstallPlanAwaitingManualApproval indicates the InstallPlan responsible for installing the current CSV is waiting 
    // for manual approval.
    SubscriptionInstallPlanAwaitingManualApproval SubscriptionConditionType = "InstallPlanAwaitingManualApproval"

    // SubscriptionInstalledCSVReplacementAvailable indicates there exists a replacement for the installed CSV.
    SubscriptionInstalledCSVReplacementAvailable SubscriptionConditionType = "InstalledCSVReplacementAvailable"

    // SubscriptionInstalledCSVMissing indicates the installed CSV is missing.
    SubscriptionInstalledCSVMissing SubscriptionConditionType = "InstalledCSVMissing"

    // SubscriptionInstalledCSVFailed indicates the installed CSV has failed.
    SubscriptionInstalledCSVFailed SubscriptionConditionType = "InstalledCSVFailed"
)

type SubscriptionCondition struct {
    // Type is the type of Subscription condition.
    Type               SubscriptionConditionType   `json:"type" description:"type of Subscription condition"`

    // Status is the status of the condition, one of True, False, Unknown.
    Status             corev1.ConditionStatus    `json:"status" description:"status of the condition, one of True, False, Unknown"`

    // Reason is a one-word CamelCase reason for the condition's last transition.
    // +optional
    Reason             string            `json:"reason,omitempty" description:"one-word CamelCase reason for the condition's last transition"`

    // Message is a human-readable message indicating details about last transition.
    // +optional
    Message            string            `json:"message,omitempty" description:"human-readable message indicating details about last transition"`

    // LastHeartbeatTime is the last time we got an update on a given condition
    // +optional
    LastHeartbeatTime  *metav1.Time  `json:"lastHeartbeatTime,omitempty" description:"last time we got an update on a given condition"`

    // LastTransitionTime is the last time the condition transit from one status to another
    // +optional
    LastTransitionTime *metav1.Time  `json:"lastTransitionTime,omitempty" description:"last time the condition transit from one status to another"`
}
```

### Subscription Reconciliation

Phasing in `SusbcriptionStatus.InstallPlanRef`:

- Create a helper function to convert `ObjectReference`s into `InstallPlanReference`s in _pkg/api/apis/operators/v1alpha1/subscription_types.go_

```go
package v1alpha1

import (
    // ...
    corev1 "k8s.io/api/core/v1"
    // ...
)
// ...
func NewInstallPlanReference(ref *corev1.ObjectReference) *InstallPlanReference {
    return &InstallPlanReference{
        APIVersion: ref.APIVersion,
        Kind:       ref.Kind,
        Name:       ref.Name,
        UID:        ref.UID,
    }
}
```

- Define an interface and method for generating `ObjectReferences` for `InstallPlan`s in _pkg/api/apis/operators/referencer.go_

```go
package operators

import (
    "fmt"
    // ...
    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/meta"
    // ...
    "github.com/operator-framework/api/pkg/operators/v1alpha1"
    "github.com/operator-framework/api/pkg/operators/v1alpha2"
)

// CannotReferenceError indicates that an ObjectReference could not be generated for a resource.
type CannotReferenceError struct{
    obj interface{}
    msg string
}

// Error returns the error's error string.
func (err *CannotReferenceError) Error() string {
    return fmt.Sprintf("cannot reference %v: %s", obj, msg)
}

// NewCannotReferenceError returns a pointer to a CannotReferenceError instantiated with the given object and message.
func NewCannotReferenceError(obj interface{}, msg string) *CannotReferenceError {
    return &CannotReferenceError{obj: obj, msg: msg}
}

// ObjectReferencer knows how to return an ObjectReference for a resource.
type ObjectReferencer interface {
    // ObjectReferenceFor returns an ObjectReference for the given resource.
    ObjectReferenceFor(obj interface{}) (*corev1.ObjectReference, error)  
}

// ObjectReferencerFunc is a function type that implements ObjectReferencer.
type ObjectReferencerFunc func(obj interface{}) (*corev1.ObjectReference, error)

// ObjectReferenceFor returns an ObjectReference for the current resource by invoking itself.
func (f ObjectReferencerFunc) ObjectReferenceFor(obj interface{}) (*corev1.ObjectReference, error) {
    return f(obj)
}

// OperatorsObjectReferenceFor generates an ObjectReference for the given resource if it's provided by the operators.coreos.com API group.
func OperatorsObjectReferenceFor(obj interface{}) (*corev1.ObjectReference, error) {
    // Attempt to access ObjectMeta
    objMeta, err := meta.Accessor(obj)
    if err != nil {
        return nil, NewCannotReferenceError(obj, err.Error())
    }

    ref := &corev1.ObjectReference{
        Namespace: objMeta.GetNamespace(),
        Name: objMeta.GetName(),
        UID: objMeta.GetUI(),
    }
    switch objMeta.(type) {
    case *v1alpha1.ClusterServiceVersion:
        ref.Kind = v1alpha1.ClusterServiceVersionKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case *v1alpha1.InstallPlan:
        ref.Kind = v1alpha1.InstallPlanKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case *v1alpha1.Subscription:
        ref.Kind = v1alpha1.SubscriptionKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case *v1alpha1.CatalogSource:
        ref.Kind = v1alpha1.CatalogSourceKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case *v1.OperatorGroup:
        ref.Kind = v1alpha2.OperatorGroupKind
        ref.APIVersion = v1alpha2.SchemeGroupVersion.String()
    case v1alpha1.ClusterServiceVersion:
        ref.Kind = v1alpha1.ClusterServiceVersionKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case v1alpha1.InstallPlan:
        ref.Kind = v1alpha1.InstallPlanKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case v1alpha1.Subscription:
        ref.Kind = v1alpha1.SubscriptionKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case v1alpha1.CatalogSource:
        ref.Kind = v1alpha1.CatalogSourceKind
        ref.APIVersion = v1alpha1.SchemeGroupVersion.String()
    case v1.OperatorGroup:
        ref.Kind = v1alpha2.OperatorGroupKind
        ref.APIVersion = v1alpha2.SchemeGroupVersion.String()
    default:
        return nil, NewCannotReferenceError(objMeta, "resource not a valid olm kind")
    }

    return ref, nil
}

type ReferenceSet map[*corev1.ObjectReference]struct{}

type ReferenceSetBuilder interface {
    Build(obj interface{}) (ReferenceSet, error)
}

type ReferenceSetBuilderFunc func(obj interface{}) (ReferenceSet, error)

func (f ReferenceSetBuilderFunc) Build(obj interface{}) (ReferenceSet, error) {
    return f(obj)
}

func BuildOperatorsReferenceSet(obj interface{}) (ReferenceSet, error) {
    referencer := ObjectReferencer(OperatorsObjectReferenceFor)
    obj := []interface{}
    set := make(ReferenceSet)
    switch v := obj.(type) {
    case []*v1alpha1.ClusterServiceVersion:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []*v1alpha1.InstallPlan:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []*v1alpha1.Subscription:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []*v1alpha1.CatalogSource:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []*v1.OperatorGroup:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []v1alpha1.ClusterServiceVersion:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []v1alpha1.InstallPlan:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []v1alpha1.Subscription:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []v1alpha1.CatalogSource:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    case []v1.OperatorGroup:
        for _, o := range v {
            ref, err := referencer.ObjectReferenceFor(o)
            if err != nil {
                return nil, err
            }
            set[ref] = struct{}{}
        }
    default:
        // Could be a single resource
        ref, err := referencer.ObjectReferenceFor(o)
        if err != nil {
            return nil, err
        }
        set[ref] = struct{}{}
    }

    return set, nil
}


```

- Add an `ObjectReferencer` field to the [catalog-operator](https://github.com/operator-framework/operator-lifecycle-manager/blob/22691a771a330fc05608a7ec1516d31a17a13ded/pkg/controller/operators/catalog/operator.go#L58)

```go
package catalog

import (
    // ...
    "github.com/operator-framework/api/pkg/operators"
    // ...
)
// ...
type Operator struct {
    // ...
    referencer operators.ObjectReferencer
}
// ...
func NewOperator(kubeconfigPath string, logger *logrus.Logger, wakeupInterval time.Duration, configmapRegistryImage, operatorNamespace string, watchedNamespaces ...string) (*Operator, error) {
    // ...
    op := &Operator{
        // ...
        referencer: operators.ObjectReferencerFunc(operators.OperatorsObjectReferenceFor),
    }
    // ...
}
//  ...
```

- Generate `ObjectReference`s in [ensureInstallPlan(...)](https://github.com/operator-framework/operator-lifecycle-manager/blob/22691a771a330fc05608a7ec1516d31a17a13ded/pkg/controller/operators/catalog/operator.go#L804)

```go
func (o *Operator) ensureInstallPlan(logger *logrus.Entry, namespace string, subs []*v1alpha1.Subscription, installPlanApproval v1alpha1.Approval, steps []*v1alpha1.Step) (*corev1.ObjectReference, error) {
    // ...
    for _, installPlan := range installPlans {
        if installPlan.Status.CSVManifestsMatch(steps) {
            logger.Infof("found InstallPlan with matching manifests: %s", installPlan.GetName())
            return a.referencer.ObjectReferenceFor(installPlan), nil
        }
    }
    // ...
}
```

Write to `SusbcriptionStatus.InstallPlan` and `SubscriptionStatus.InstallPlanRef`:

- Generate `ObjectReference`s in [createInstallPlan(...)](https://github.com/operator-framework/operator-lifecycle-manager/blob/22691a771a330fc05608a7ec1516d31a17a13ded/pkg/controller/operators/catalog/operator.go#L863)

```go
func (o *Operator) createInstallPlan(namespace string, subs []*v1alpha1.Subscription, installPlanApproval v1alpha1.Approval, steps []*v1alpha1.Step) (*corev1.ObjectReference, error) {
    // ...
    return a.referencer.ObjectReferenceFor(res), nil
}
```

- Use `ObjectReference` to populate both `SusbcriptionStatus.InstallPlan` and `SubscriptionStatus.InstallPlanRef` in [updateSubscriptionStatus](https://github.com/operator-framework/operator-lifecycle-manager/blob/22691a771a330fc05608a7ec1516d31a17a13ded/pkg/controller/operators/catalog/operator.go#L774)

```go
func (o *Operator) updateSubscriptionStatus(namespace string, subs []*v1alpha1.Subscription, installPlanRef *corev1.ObjectReference) error {
    // ...
    for _, sub := range subs {
        // ...
        if installPlanRef != nil {
            sub.Status.InstallPlanRef = installPlanRef
            sub.Status.Install = v1alpha1.NewInstallPlanReference(installPlanRef)
            sub.Status.State = v1alpha1.SubscriptionStateUpgradePending
        }
        // ...
    }
    // ...
}
```

Phase in orthogonal `SubscriptionStatus` condition updates (pick a condition type to start with):

- Pick `SubscriptionCatalogSourcesUnhealthy`
- Add `SusbcriptionCondition` getter and setter helper methods to `SubscriptionStatus`

```go
// GetCondition returns the SubscriptionCondition of the given type if it exists in the SubscriptionStatus' Conditions; returns a condition of the given type with a ConditionStatus of "Unknown" if not found.
func (status SubscriptionStatus) GetCondition(conditionType SubscriptionConditionType) SubscriptionCondition {
    for _, cond := range status.Conditions {
        if cond.Type == conditionType {
            return cond
        }
    }

    return SubscriptionCondition{
        Type: conditionType,
        Status: corev1.ConditionUnknown,
        // ...
    }
}

// SetCondition sets the given SubscriptionCondition in the SubscriptionStatus' Conditions.
func (status SubscriptionStatus) SetCondition(condition SubscriptionCondition) {
    for i, cond := range status.Conditions {
        if cond.Type == condition.Type {
            cond[i] = condition
            return
        }
    }

    status.Conditions = append(status.Conditions, condition)
}
```

- Add a `ReferenceSetBuilder` field to the [catalog-operator](https://github.com/operator-framework/operator-lifecycle-manager/blob/22691a771a330fc05608a7ec1516d31a17a13ded/pkg/controller/operators/catalog/operator.go#L58)

```go
package catalog

import (
    // ...
    "github.com/operator-framework/api/pkg/operators"
    // ...
)
// ...
type Operator struct {
    // ...
    referenceSetBuilder operators.ReferenceSetBuilder
}
// ...
func NewOperator(kubeconfigPath string, logger *logrus.Logger, wakeupInterval time.Duration, configmapRegistryImage, operatorNamespace string, watchedNamespaces ...string) (*Operator, error) {
    // ...
    op := &Operator{
        // ...
        referenceSetBuilder: operators.ReferenceSetBuilderFunc(operators.BuildOperatorsReferenceSet),
    }
    // ...
}
//  ...
```

- Define a new `CatalogSource` sync function that checks the health of a given `CatalogSource` and the health of every `CatalogSource` in its namespace and the global namespace and updates all `Subscription`s that have visibility on it with the condition state

```go
// syncSusbcriptionCatalogStatus generates a SubscriptionCatalogStatus for a CatalogSource and updates the
// status of all Subscriptions in its namespace; for CatalogSources in the global catalog namespace, Subscriptions
// in all namespaces are updated.
func (o *Operator) syncSubscriptionCatalogStatus(obj interface{}) (syncError error) {
    catsrc, ok := obj.(*v1alpha1.CatalogSource)
    if !ok {
        o.Log.Debugf("wrong type: %#v", obj)
        return fmt.Errorf("casting CatalogSource failed")
    }

    logger := o.Log.WithFields(logrus.Fields{
        "catsrc": catsrc.GetName(),
        "namespace": catsrc.GetNamespace(),
        "id":     queueinformer.NewLoopID(),
    })
    logger.Debug("syncing subscription catalogsource status")

    // Get SubscriptionCatalogStatus
    sourceKey := resolver.CatalogKey{Name: owner.Name, Namespace: metaObj.GetNamespace()}
    status := o.getSubscriptionCatalogStatus(logger, sourceKey, a.referencer.ObjectReferenceFor(catsrc))

    // Update the status of all Subscriptions that can view this CatalogSource
    syncError = updateSubscriptionCatalogStatus(logger, status)
}

// getSubscriptionCatalogStatus gets the SubscriptionCatalogStatus for a given SourceKey and ObjectReference.
func (o *Operator) getSubscriptionCatalogStatus(logger logrus.Entry, sourceKey resolver.SourceKey, *corev1.ObjectReference) *v1alpha1.SubscriptionCatalogStatus {
    // TODO: Implement this
}

// updateSubscriptionCatalogStatus updates all Subscriptions in the CatalogSource namespace with the given SubscriptionCatalogStatus;
// for CatalogSources in the global catalog namespace, Subscriptions in all namespaces are updated.
func (o *Operator) updateSubscriptionCatalogStatus(logger logrus.Entry, status SubscriptionCatalogStatus) error {
    // TODO: Implement this. It should handle removing CatalogStatus entries to non-existent CatalogSources.
}
```

- Define a new `Subscription` sync function that checks the `CatalogStatus` field and sets `SubscriptionCondition`s relating to `CatalogSource` status

```go
func (o *Operator) syncSubscriptionCatalogConditions(obj interface{}) (syncError error) {
    sub, ok := obj.(*v1alpha1.Subscription)
    if !ok {
        o.Log.Debugf("wrong type: %#v", obj)
        return fmt.Errorf("casting Subscription failed")
    }

    logger := o.Log.WithFields(logrus.Fields{
        "sub": sub.GetName(),
        "namespace": sub.GetNamespace(),
        "id":     queueinformer.NewLoopID(),
    })
    logger.Debug("syncing subscription catalogsource conditions")

    // Get the list of CatalogSources visible to the Subscription
    catsrcs, err := o.listResolvableCatalogSources(sub.GetNamespace())
    if err != nil {
        logger.WithError(err).Warn("could not list resolvable catalogsources")
        syncError = err
        return
    }

    // Build reference set from resolvable catalogsources
    refSet, err := o.referenceSetBuilder.Build(catsrcs)
    if err != nil {
        logger.WithError(err).Warn("could not build object reference set of resolvable catalogsources")
        syncError = err
        return
    }

    // Defer an update to the Subscription
    out := sub.DeepCopy()
    defer func() {
        // TODO: Implement update SubscriptionStatus using out if syncError == nil and Subscription has changed
    }()

    // Update CatalogSource related CatalogSourceConditions
    currentSources = len(refSet)
    knownSources = len(sub.Status.CatalogStatus)

    // unhealthyUpdated is set to true when a change has been made to the condition of type SubscriptionCatalogSourcesUnhealthy
    unhealthyUpdated := false
    // TODO: Add flags for other condition types

    if currentSources > knownSources {
        // Flip SubscriptionCatalogSourcesUnhealthy to "Unknown"
        condition := out.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
        condition.Status = corev1.ConditionUnknown
        condition.Reason = "MissingCatalogInfo"
        condition.Message = fmt.Sprintf("info on health of %d/%d catalogsources not yet known", currentSources - knownSources, currentSources)
        condition.LastSync = timeNow()
        out.Status.SetCondition(condition)
        unhealthyUpdated = true
    }

    // TODO: Add flags for other condition types to loop predicate
    for i := 0; !unhealthyUpdated && i < knownSources; i++ {
        status := sub.Status.CatalogSources

        if !unhealthyUpdated {
            if status.CatalogSourceRef == nil {
                // Flip SubscriptionCatalogSourcesUnhealthy to "Unknown"
                condition := out.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
                condition.Status = corev1.ConditionUnknown
                condition.Reason = "CatalogInfoInvalid"
                condition.Message = "info missing reference to catalogsource"
                condition.LastSync = timeNow()
                out.Status.SetCondition(condition)
                unhealthyUpdated = true
                break
            }

            if _, ok := refSet[status.CatalogSourceRef]; !ok {
                // Flip SubscriptionCatalogSourcesUnhealthy to "Unknown"
                condition := out.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
                condition.Status = corev1.ConditionUnknown
                condition.Reason = "CatalogInfoInconsistent"
                condition.Message = fmt.Sprintf("info found for non-existent catalogsource %s/%s", ref.Name, ref.Namespace)
                condition.LastSync = timeNow()
                out.Status.SetCondition(condition)
                unhealthyUpdated = true
                break
            }

            if !status.CatalogSourceRef.Healthy {
                // Flip SubscriptionCatalogSourcesUnhealthy to "True"
                condition := out.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
                condition.Status = corev1.ConditionTrue
                condition.Reason = "CatalogSourcesUnhealthy"
                condition.Message = "one or more visible catalogsources are unhealthy"
                condition.LastSync = timeNow()
                out.Status.SetCondition(condition)
                unhealthyUpdated = true
                break
            }
        }

        // TODO: Set any other conditions relating to the CatalogSource status
    }

    if !unhealthyUpdated {
        // Flip SubscriptionCatalogSourcesUnhealthy to "False"
        condition := out.Status.GetCondition(v1alpha1.SubscriptionCatalogSourcesUnhealthy)
        condition.Status = corev1.ConditionFalse
        condition.Reason = "CatalogSourcesHealthy"
        condition.Message = "all catalogsources are healthy"
        condition.LastSync = timeNow()
        out.Status.SetCondition(condition)
        unhealthyUpdated = true
    }
}

// listResolvableCatalogSources returns a list of the CatalogSources that can be used in resolution for a Subscription in the given namespace.
func (o *Operator) listResolvableCatalogSources(namespace string) ([]v1alpha1.CatalogSource, error) {
    // TODO: Implement this. Should be the union of CatalogSources in the given namespace and the global catalog namespace.
}
```

- Register new [QueueIndexer](https://github.com/operator-framework/operator-lifecycle-manager/blob/a88f5349eb80da2367b00a5191c0a7b50074f331/pkg/lib/queueinformer/queueindexer.go#L14)s with separate workqueues for handling `syncSubscriptionCatalogStatus` and `syncSubscriptionCatalogConditions` to the [catalog-operator](https://github.com/operator-framework/operator-lifecycle-manager/blob/22691a771a330fc05608a7ec1516d31a17a13ded/pkg/controller/operators/catalog/operator.go#L58). Use the same cache feeding other respective workqueues.

```go
package catalog
// ...
type Operator struct {
    // ...
    subscriptionCatalogStatusIndexer *queueinformer.QueueIndexer
    subscriptionStatusIndexer *queueinformer.QueueIndexer
}
// ...
func NewOperator(kubeconfigPath string, logger *logrus.Logger, wakeupInterval time.Duration, configmapRegistryImage, operatorNamespace string, watchedNamespaces ...string) (*Operator, error) {
    // ...
    // Register separate queue for syncing SubscriptionStatus from CatalogSource updates
    subCatStatusQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "subCatStatus")
    subCatQueueIndexer := queueinformer.NewQueueIndexer(subCatStatusQueue, op.catsrcIndexers, op.syncSubscriptionCatalogStatus, "subCatStatus", logger, metrics.NewMetricsNil())
    op.RegisterQueueIndexer(subCatQueueIndexer)
    op.subscriptionCatalogStatusIndexer = subCatQueueIndexer
    // ...
    // Register separate queue for syncing SubscriptionStatus
    subStatusQueue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "subStatus")
    subQueueIndexer := queueinformer.NewQueueIndexer(csvStatusQueue, op.subIndexers, op.syncSubscriptionCatalogConditions, "subStatus", logger, metrics.NewMetricsNil())
    op.RegisterQueueIndexer(subQueueIndexer)
    op.subscriptionStatusIndexer = subQueueIndexer
    // ...
}
//  ...
```