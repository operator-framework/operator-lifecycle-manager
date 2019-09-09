# CSV Reporting

## Motivation
The `ClusterServiceVersion` `CustomResource` needs to report useful and contextual information to the user via the `status` sub-resource. An end user associates an operator with a `CSV`. The end user is primarily interested in learning about the status of the deployment or upgrade of the operator associated with the `CSV`. The following events among many others are of interest

* An instance of the operator managed by the `csv` is being installed (No previous version exists).
* An operator is being upgraded to a desired version.
* An operator has been successfully installed or upgraded.
* Error happens while an operator install or upgrade is in progress.
* An operator is being removed.

### Conventions
In order to design a status that makes sense in the context of kubernetes resources, it's important to conform to current conventions. This will also help us avoid pitfalls that may have already been solved.

In light of this, `ClusterServiceVesrion` will have the following `Condition` type(s).

```go
// ClusterServiceVersionConditionType is the state of the underlying operator. 
type ClusterServiceVersionConditionType string

const (
  // Available means that the underlying operator has been deployed successfully
  // and it has passed all liveness/readiness check(s) performed by olm.
  OperatorAvailable ClusterServiceVersionConditionType = "Available"

  // Progressing means that the deployment of the underlying operator is in progress.
  OperatorProgressing ClusterServiceVersionConditionType = "Progressing"
  
  // We can add more condition type(s) as wee see fit.
)
```

The current definition of `ClusterServiceVersionCondition` does not conform to kubernetes `status` conventions. We will make the following change(s) to make it conformant to current conventions.
* Remove `LastUpdateTime` from `ClusterServiceVersionCondition`. There is no logic that depends on this field.
* Remove `Phase`
* Add `Type` of `ClusterServiceVersionConditionType` type.
* Add `Status` of `corev1.ConditionStatus` type.

```go
import (
    corev1 "k8s.io/kubernetes/pkg/apis/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ClusterServiceVersionCondition struct {
    // Type is the type of ClusterServiceVersionCondition condition.
    Type               ClusterServiceVersionConditionType   `json:"type" description:"type of ClusterServiceVersion condition"`

    // Status is the status of the condition, one of True, False, Unknown.
    Status             corev1.ConditionStatus    `json:"status" description:"status of the condition, one of True, False, Unknown"`

    // Reason is a one-word CamelCase reason for the condition's last transition.
    // +optional
    Reason             ConditionReason            `json:"reason,omitempty" description:"one-word CamelCase reason for the condition's last transition"`

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

### Current Phase
A CSV transitions through a set of phase(s) during its life cycle. These phase(s) are internal to olm. Currently the following group of fields track the current phase.
```go
type ClusterServiceVersionStatus struct {
	// Current condition of the ClusterServiceVersion
	Phase ClusterServiceVersionPhase `json:"phase,omitempty"`
	// A human readable message indicating details about why the ClusterServiceVersion is in this condition.
	// +optional
	Message string `json:"message,omitempty"`
	// A brief CamelCase message indicating details about why the ClusterServiceVersion is in this state.
	// e.g. 'RequirementsNotMet'
	// +optional
	Reason ConditionReason `json:"reason,omitempty"`
	// Last time we updated the status
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	// Last time the status transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}
```

We can consolidate this set fields into a separate structure as follows
```go
type ClusterServiceVersionTransition struct {
	// Name of the phase. 
	Phase ClusterServiceVersionPhase `json:"phase,omitempty"`
	// A human readable message indicating details about why the ClusterServiceVersion is in this phase.
	// +optional
	Message string `json:"message,omitempty"`
	// A brief CamelCase message indicating details about why the ClusterServiceVersion is in this phase.
	// e.g. 'RequirementsNotMet'
	// +optional
	Reason ConditionReason `json:"reason,omitempty"`
	// Last time we updated the status
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	// Last time the status transitioned to this phase.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

type ClusterServiceVersionStatus struct {
  CurrentPhase ClusterServiceVersionTransition `json:"phase,omitempty"`
}
```

This will probably break the current UI since it expects the `phase` name to be directly under `status` for any CSV.


### Transition History
Today we keep appending new `ClusterServiceVersionCondition` to `Status.Conditions`. This does not conform to the conventions. `Status.Conditions` will now be a collection of `ClusterServiceVersionCondition` last observed. Typically it will have an item of `ClusterServiceVersionCondition` from each `ClusterServiceVersionConditionType`, as shown below.

```yaml
conditions:  
  - type: Progressing
    Status: True
    Message: Working towards v1.0.0
  - type: Available
    Status: False
    Reason: CSVReasonRequirementsUnknown
    Message: Scheduling ClusterServiceVersion for requirement verification
```

If we want to continue to maintain a history of last N phase transition(s) or activities then we can add the following to `status`.
```go
type ClusterServiceVersionStatus struct {
  LastTransitions []ClusterServiceVersionTransition `json:"lastTransitions,omitempty"`
}
```

### Versions
The `ClusterServiceVersion` resource needs to report the version of the operator it manages. The version information should have a `name` and a `version`. It must always match the currently installed version. If `v1.0.0` is currently installed, then this must indicate `v1.0.0` even if the associated `ClusterServiceVersion` is in the process of installing a new version `v1.1.0`.

```go
type OperatorVersion struct {
    // Name is the name of the operator.
    Name string `json:"name"`

    // Version of the operator currently installed.
    Version string `json:"version"`
}
```

```go
type ClusterServiceVersionStatus struct {    
  // List of conditions, a history of state transitions
  Conditions []ClusterServiceVersionCondition `json:"conditions,omitempty"`
    
  // Version of the underlying operator.
  Version OperatorVersion `json:"version,omitempty"`
}
```

### Related Objects
If an end user is looking at a csv, he/she should be able to access the related resource(s) associated with the csv. For example, the end user should be able to:
* Refer to the `Subscription` object that is associated with the `csv`.
* Refer to the `InstallPlan` object that created this `csv`. Is this useful during troubleshooting?
* Refer to the `CatalogSource` object that contains the operator manifest.

To make it easier for the end user to access the related resource(s), the following change is being proposed.
```go
type ClusterServiceVersionStatus struct {
  // Option 1: 
  
  // InstallPlanRef is a reference to the InstallPlan that created this CSV.
  // +optional
  InstallPlanRef *corev1.ObjectReference `json:"installPlanRef,omitempty"`
  
  // SubscriptionRef is a reference to the Subscription related to this CSV.
  // +optional
  SubscriptionRef *corev1.ObjectReference `json:"subscriptionRef,omitempty"`  

  // CatalogSourceRef is a reference to the CatalogSource related to this CSV.
  // +optional
  CatalogSourceRef *corev1.ObjectReference `json:"catalogSourceRef,omitempty"`

  // Option 2
  // An array of ObjectReference pointing to the related object(s)
  RelatedObjects []*corev1.ObjectReference `json:"relatedObjects,omitempty"`
}
```

## User Experience
Let's go through some of the use cases related to operator deployment and upgrade and see what portion of the `status` would look like to an end user/administrator. 

### Use Case 1:
A new operator is being installed and olm is running requirements check.
```yaml
status:
  ...
  phase: Pending 
  conditions:
  - type: Progressing
    Status: True
    Message: Working towards v1.0.0
  - type: Available
    Status: False
    Reason: CSVReasonRequirementsUnknown
    Message: Scheduling ClusterServiceVersion for requirement verification
```
`status.version` is empty since we don't have any version of the operator installed yet.

### Use Case 2: 
A new operator has been successfully installed, no previous version existed.
```yaml
status:
  ...
  phase: Succeeded 
  conditions:
  - type: Progressing
    Status: False
    Message: Deployed version v1.0.0
  - type: Available
    Status: True
  version:
    name: etcd
    version: 1.0.0
```

### Use Case 3: 
An existing operator is being upgraded to a new version. We need to put more thoughts into this, what is below is just a rough sketch.
```yaml
// This is while upgrade is in progress.
// Original ClusterServiceVersion that is being replaced.
status:
  ...
  phase: Replacing
  conditions:  
  - type: Progressing
    Status: False
  - type: Available
    Status: False
    Reason: BeingReplaced
  version:
    name: etcd
    version: v1.0.0

// Head CSV
status:
  ...
  phase: Pending 
  conditions:
  - type: Progressing
    Status: True
    Message: Working toward v2.0.0
  - type: Available
    Status: False
  version:
    name: etcd
    version: 1.0.0
```
`version` is set to `1.0.0` since this is the last version installed on the cluster. Once the upgrade is successful, the `head` csv status will look like this.
```yaml
status:
  ...
  phase: Succeeded 
  conditions:
  - type: Progressing
    Status: False
    Message: Deployed version v2.0.0
  - type: Available
    Status: True
  version:
    name: etcd
    version: 2.0.0
```