package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	GroupVersion = "v1alpha1" // used in registering InstallPlan scheme

	InstallPlanCRDName       = "installplan-v1s.app.coreos.com"
	InstallPlanCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// Approval is the user approval policy for an InstallPlan.
type Approval string

const (
	ApprovalAutomatic  Approval = "Automatic"
	ApprovalUpdateOnly Approval = "Update-Only"
	ApprovalManual     Approval = "Manual"
)

// InstallPlanPhase is the current status of a InstallPlan as a whole.
type InstallPlanPhase string

const (
	InstallPlanPhasePlanning         InstallPlanPhase = "Planning"
	InstallPlanPhaseRequiresApproval InstallPlanPhase = "RequiresApproval"
	InstallPlanPhaseInstalling       InstallPlanPhase = "Installing"
	InstallPlanPhaseComplete         InstallPlanPhase = "Complete"
)

// StepStatus is the current status of a particular resource an in
// InstallPlan.
type StepStatus string

const (
	StepStatusUnknown    StepStatus = "Unknown"
	StepStatusNotPresent StepStatus = "NotPresent"
	StepStatusPresent    StepStatus = "Present"
	StepStatusCreated    StepStatus = "Created"
)

// ConditionReason is a camelcased reason for the state transition.
type InstallPlanConditionReason string

const (
	InstallPlanReasonPlanUnknown        InstallPlanConditionReason = "PlanUnknown"
	InstallPlanReasonDependencyConflict InstallPlanConditionReason = "DependenciesConflict"
	InstallPlanReasonComponentFailed    InstallPlanConditionReason = "InstallComponentFailed"
	InstallPlanReasonInstallSuccessful  InstallPlanConditionReason = "InstallSucceeded"
	InstallPlanReasonInstallCheckFailed InstallPlanConditionReason = "InstallCheckFailed"
)

// InstallPlanSpec defines a set of Application resources to be installed
type InstallPlanSpec struct {
	ClusterServiceVersionNames []string `json:"clusterServiceVersionNames"`
	Approval                   Approval `json:"approval"`
}

// InstallPlanStatus represents the information about the status of
// steps required to complete installation.
//
// Status may trail the actual state of a system.
type InstallPlanStatus struct {
	Phase              InstallPlanPhase           `json:"phase,omitempty"`
	Message            string                     `json:"message,omitempty"`
	Reason             InstallPlanConditionReason `json:"reason,omitempty"`
	LastUpdateTime     metav1.Time                `json:"lastUpdateTime,omitempty"`
	LastTransitionTime metav1.Time                `json:"lastTransitionTime,omitempty"`
	Conditions         []InstallPlanCondition     `json:"conditions,omitempty"`
	Plan               []Step                     `json:"plan,omitempty"`
}

// InstallPlanConditions represents the overall status of the execution of
// an InstallPlan.
type InstallPlanCondition struct {
	Phase              InstallPlanPhase           `json:"phase,omitempty"`
	Message            string                     `json:"message,omitempty"`
	Reason             InstallPlanConditionReason `json:"reason,omitempty"`
	LastUpdateTime     metav1.Time                `json:"lastUpdateTime,omitempty"`
	LastTransitionTime metav1.Time                `json:"lastTransitionTime,omitempty"`
}

// Step represents the status of an individual step in an InstallPlan.
type Step struct {
	Resolving string       `json:"resolving"`
	Resource  StepResource `json:"resource"`
	Status    StepStatus   `json:"status"`
}

// StepResource represents the status of a resource to be tracked by an
// InstallPlan.
type StepResource struct {
	Group    string `json:"group"`
	Version  string `json:"version"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Manifest string `json:"manifest"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "InstallPlanSpec"
type InstallPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *InstallPlanSpec `json:"spec"`
	Status metav1.Status    `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type InstallPlanList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*InstallPlanSpec `json:"items"`
}
