package v1alpha1

import (
	"bytes"
	"errors"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	k8sjson "k8s.io/apimachinery/pkg/runtime/serializer/json"

	csvv1alpha1 "github.com/coreos-inc/alm/pkg/apis/clusterserviceversion/v1alpha1"
)

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
	InstallPlanPhaseNone             InstallPlanPhase = ""
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

// ErrInvalidInstallPlan is the error returned by functions that operate on
// InstallPlans when the InstallPlan does not contain totally valid data.
var ErrInvalidInstallPlan = errors.New("the InstallPlan contains invalid data")

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
	InstallPlanCondition `json:",inline"`
	Conditions           []InstallPlanCondition `json:"conditions,omitempty"`
	Plan                 []Step                 `json:"plan,omitempty"`
}

// InstallPlanCondition represents the overall status of the execution of
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

// NewStepResourceFromCSV creates an unresolved Step for the provided CSV.
func NewStepResourceFromCSV(csv *csvv1alpha1.ClusterServiceVersion) (StepResource, error) {
	csvScheme := runtime.NewScheme()
	if err := csvv1alpha1.AddToScheme(csvScheme); err != nil {
		return StepResource{}, err
	}
	csvSerializer := json.NewSerializer(json.DefaultMetaFactory, csvScheme, csvScheme, true)

	var manifestCSV bytes.Buffer
	if err := csvSerializer.Encode(csv, &manifestCSV); err != nil {
		return StepResource{}, err
	}

	step := StepResource{
		Name:     csv.Name,
		Kind:     csv.Kind,
		Group:    csv.GroupVersionKind().Group,
		Version:  csv.GroupVersionKind().Version,
		Manifest: manifestCSV.String(),
	}

	return step, nil
}

// NewStepResourceFromCRD creates an unresolved Step for the provided CRD.
func NewStepResourceFromCRD(crd *v1beta1.CustomResourceDefinition) (StepResource, error) {
	crdSerializer := k8sjson.NewSerializer(k8sjson.DefaultMetaFactory, scheme.Scheme, scheme.Scheme, true)

	var manifest bytes.Buffer
	if err := crdSerializer.Encode(crd, &manifest); err != nil {
		return StepResource{}, err
	}

	step := StepResource{
		Name:     crd.Name,
		Kind:     crd.Kind,
		Group:    crd.Spec.Group,
		Version:  crd.Spec.Version,
		Manifest: manifest.String(),
	}

	return step, nil
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "InstallPlanSpec"
type InstallPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   InstallPlanSpec   `json:"spec"`
	Status InstallPlanStatus `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type InstallPlanList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []InstallPlan `json:"items"`
}
