// Package v1alpha1 implements all the required types and methods for parsing
// resources for v1alpha1 versioned ClusterServiceVersions.
package v1alpha1

import (
	"encoding/json"

	"github.com/coreos/go-semver/semver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GroupVersion is the version used in the Scheme for ClusterServiceVersions.
const GroupVersion = "v1alpha1"

// NamedInstallStrategy represents the block of an ClusterServiceVersion resource
// where the install strategy is specified.
type NamedInstallStrategy struct {
	StrategyName    string          `json:"strategy"`
	StrategySpecRaw json.RawMessage `json:"spec"`
}

// CustomResourceDefinitions declares all of the CRDs managed or required by
// an operator being ran by ClusterServiceVersion.
//
// If the CRD is present in the Owned list, it is implicitly required.
type CustomResourceDefinitions struct {
	Owned    []string `json:"owned"`
	Required []string `json:"required"`
}

// ClusterServiceVersionSpec declarations tell the ALM how to install an operator
// that can manage apps for given version and AppType.
type ClusterServiceVersionSpec struct {
	InstallStrategy           NamedInstallStrategy      `json:"install"`
	Version                   semver.Version            `json:"version"`
	Maturity                  string                    `json:"maturity"`
	CustomResourceDefinitions CustomResourceDefinitions `json:"customresourcedefinitions"`
	Permissions               []string                  `json:"permissions"`
	DisplayName               string                    `json:"displayName"`
	Description               string                    `json:"description"`
	Keywords                  []string                  `json:"keywords"`
	Maintainers               []Maintainer              `json:"maintainers"`
	Links                     []AppLink                 `json:"links"`
	Icon                      Icon                      `json:"iconURL"`

	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,11,rep,name=labels"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata. They are not
	// queryable and should be preserved when modifying objects.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/annotations/
	// +optional
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,12,rep,name=annotations"`

	// Label selector for pods. Existing ReplicaSets whose pods are
	// selected by this will be the ones affected by this deployment.
	// +optional
	Selector *metav1.LabelSelector `json:"selector,omitempty" protobuf:"bytes,2,opt,name=selector"`
}

type Maintainer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type AppLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Icon struct {
	Data      string `json:"base64data"`
	MediaType string `json:"mediatype"`
}

// ClusterServiceVersionPhase is a label for the condition of a ClusterServiceVersion at the current time.
type ClusterServiceVersionPhase string

// These are the valid phases of ClusterServiceVersion
const (
	// CSVPending means the csv has been accepted by the system, but the install strategy has not been attempted.
	// This is likely because there are unmet requirements.
	CSVPending ClusterServiceVersionPhase = "Pending"
	// CSVRunning means that the install strategy was successful and the Cloud Service is now available in namespace.
	CSVRunning ClusterServiceVersionPhase = "Running"
	// CSVSucceeded means that the resources in the CSV were created successfully.
	CSVSucceeded ClusterServiceVersionPhase = "Succeeded"
	// CSVFailed means that the install strategy could not be successfully completed.
	CSVFailed ClusterServiceVersionPhase = "Failed"
	// CSVUnknown means that for some reason the state of the csv could not be obtained.
	CSVUnknown ClusterServiceVersionPhase = "Unknown"
)

type ConditionStatus string

// These are valid condition statuses. "ConditionTrue" means a resource is in the condition.
// "ConditionFalse" means a resource is not in the condition. "ConditionUnknown" means kubernetes
// can't decide if a resource is in the condition or not. In the future, we could add other
// intermediate conditions, e.g. ConditionDegraded.
const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// CSVConditionType is a valid value for ClusterServiceVersionCondition.Type
type CSVConditionType string

// These are valid conditions of ClusterServiceVersion.
const (
	// CSVAvailable means the service is running and available in the namespace.
	CSVAvailable CSVConditionType = "Available"
	// CSVUnavailable means the service is not available.
	CSVUnavailable CSVConditionType = "Unavailable"
)

// PodCondition contains details for the current condition of this pod.
type ClusterServiceVersionCondition struct {
	// Type is the type of the condition.
	Type CSVConditionType `json:"type" protobuf:"bytes,1,opt,name=type,casttype=PodConditionType"`
	// Status is the status of the condition.
	// Can be True, False, Unknown.
	Status ConditionStatus `json:"status" protobuf:"bytes,2,opt,name=status,casttype=ConditionStatus"`
	// Last time we probed the condition.
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty" protobuf:"bytes,3,opt,name=lastProbeTime"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty" protobuf:"bytes,4,opt,name=lastTransitionTime"`
	// Unique, one-word, CamelCase reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,5,opt,name=reason"`
	// Human-readable message indicating details about last transition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,6,opt,name=message"`
}

// ClusterServiceVersionStatus represents information about the status of a pod. Status may trail the actual
// state of a system.
type ClusterServiceVersionStatus struct {
	// Current condition of the ClusterServiceVersion
	Phase ClusterServiceVersionPhase `json:"phase,omitempty" protobuf:"bytes,1,opt,name=phase,casttype=PodPhase"`
	// Current service state of the the ClusterServiceVersion.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []ClusterServiceVersionCondition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,2,rep,name=conditions"`
	// A human readable message indicating details about why the ClusterServiceVersion is in this condition.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,3,opt,name=message"`
	// A brief CamelCase message indicating details about why the ClusterServiceVersion is in this state.
	// e.g. 'Evicted'
	// +optional
	Reason string `json:"reason,omitempty" protobuf:"bytes,4,opt,name=reason"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ClusterServiceVersion is a Custom Resource of type `ClusterServiceVersionSpec`.
type ClusterServiceVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ClusterServiceVersionSpec   `json:"spec"`
	Status ClusterServiceVersionStatus `json:"status"`
}

// ClusterServiceVersionList represents a list of ClusterServiceVersions.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterServiceVersionList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []ClusterServiceVersion `json:"items"`
}
