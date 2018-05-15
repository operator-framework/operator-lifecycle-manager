package types

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// TectonicAPIGroup is the APIGroup of the Tectonic CRDs.
	TectonicAPIGroup = "tco.coreos.com"
	// TectonicNamespace is the namespace of the Tectonic CRDs and operators.
	TectonicNamespace = "tectonic-system"
)

const (
	// TectonicVersionGroupVersion is the group version of the TectonicVersion CRD.
	TectonicVersionGroupVersion = "v1"
	// TectonicVersionKind is the Kind name of the TectonicVersion CRD.
	TectonicVersionKind = "TectonicVersion"
)

const (
	// AppVersionGroupVersion is the group version of the AppVersion CRD.
	AppVersionGroupVersion = "v1"
	// AppVersionKind is the name of the AppVersion CRD.
	AppVersionKind = "AppVersion"

	// AppVersionNameTectonicCluster is the Object name of the AppVersion
	// CRD for the Tectonic cluster,
	// which contains the current/desired/target versions.
	AppVersionNameTectonicCluster = "tectonic-cluster"

	// AppVersionNameKubernetes is the Object name of the AppVersion
	// CRD for the Kubernetes operator,
	// which contains the current/desired/target versions.
	AppVersionNameKubernetes = "kubernetes"
)

// Old constants for TPRs.
// TODO(yifan): DEPRECATED, remove after migration is completed.
const (
	// TectonicTPRAPIGroup is the APIGroup of the Tectonic TPRs.
	TectonicTPRAPIGroup = "coreos.com"

	// TectonicVersionTPRGroupVersion is the group version of the TectonicVersion TPR.
	TectonicVersionTPRGroupVersion = "v1"

	// AppVersionTPRGroupVersion is the version of the AppVersion TPR.
	AppVersionTPRGroupVersion = "v1"
)

// Pre-defined failure types.
const (
	// FailureTypeUpdateFailed represents an error that occured during an
	// update from which we cannot recover.
	FailureTypeUpdateFailed FailureType = "Update failed" // This may be used to describe failures that can be recovered after restoring from backup.
	// FailureTypeUpdateCannotProceed represents a failure not caused by
	// an operator failure, but that otherwise causes the update to not
	// proceed.
	FailureTypeUpdateCannotProceed FailureType = "Update cannot proceed"
	// FailureTypeHumanDecisionNeeded represents an error which must be
	// resolved by a human before the update can proceed.
	FailureTypeHumanDecisionNeeded FailureType = "Human decision needed"
	// FailureTypeVoidedWarranty represents a failure to update due to
	// modifications to the cluster that have voided the warranty.
	FailureTypeVoidedWarranty FailureType = "Voided warranty"
	// FailureTypeUpdatesNotPossible represents a failure type which expresses
	// that an update is not possible, such as updating from an unsupported
	// version.
	FailureTypeUpdatesNotPossible FailureType = "Updates are not possible"
)

// AppVersion represents the AppVersion CRD object, it only
// contains the required fields. It doesn't include operator specific
// fields. So update and write back this object directly might cause information
// loss.
type AppVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec        AppVersionSpec   `json:"spec"`
	Status      AppVersionStatus `json:"status"`
	UpgradeReq  int              `json:"upgradereq"`
	UpgradeComp int              `json:"upgradecomp"`
}

// AppVersionSpec is the "spec" part of the AppVersion CRD.
type AppVersionSpec struct {
	DesiredVersion string `json:"desiredVersion"`
	Paused         bool   `json:"paused"`
}

// AppVersionStatus is the "status" part of the AppVersion CRD.
type AppVersionStatus struct {
	CurrentVersion string `json:"currentVersion"`
	TargetVersion  string `json:"targetVersion"`
	// If non-empty, then the upgrade is considered as a failure.
	// Detailed information is embeded in this field.
	FailureStatus *FailureStatus `json:"failureStatus,omitempty"`
	Paused        bool           `json:"paused"`
	TaskStatuses  []TaskStatus   `json:"taskStatuses"`
}

// TaskState is the update state of an update task.
type TaskState string

const (
	// States of each update task.

	// TaskStateNotStarted means the update task has started yet.
	// All update tasks will be set to this state at the start of the update process.
	TaskStateNotStarted TaskState = "NotStarted"

	// TaskStateRunning means the update task is in progress.
	TaskStateRunning TaskState = "Running"

	// TaskStateCompleted means the update task is succesfully completed.
	TaskStateCompleted TaskState = "Completed"

	// TaskStateFailed means the update task failed.
	TaskStateFailed TaskState = "Failed"

	// TaskStateBackOff means the update task is in back-off because it
	// has failed last time and is going to try again.
	TaskStateBackOff TaskState = "BackOff"
)

// TaskStatus represents the status of an update task.
type TaskStatus struct {
	// Name is the name of the task, e.g. "Update kube-version-operator".
	Name string `json:"name"`
	// Type is the type of the task, e.g. "operator", "cleanup". This is for grouping up the tasks.
	Type string `json:"type"`
	// State is the current state of the task, e.g. "Running".
	State TaskState `json:"state"`
	// Reason is a strings that indicates why the task is in current state, e.g. "Version not supported".
	Reason string `json:"reason"`
}

// FailureType is a human readable string to for pre-defined failure types.
type FailureType string

// FailureStatus represents the failure information.
type FailureStatus struct {
	Type   FailureType `json:"type"`
	Reason string      `json:"reason"`
}

// String returns a stringified failure status including Type and Reason.
func (f FailureStatus) String() string {
	return fmt.Sprintf("%s: %s", f.Type, f.Reason)
}

// AppVersionList represents a list of AppVersion CRD objects that will
// be returned from a List() operation.
type AppVersionList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []AppVersion `json:"items"`
}

// AppVersionModifier is a modifier function to be used when atomically
// updating an AppVersion CRD.
type AppVersionModifier func(*AppVersion) error
