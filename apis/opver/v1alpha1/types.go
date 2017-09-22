// Package v1alpha1 implements all the required types and methods for parsing
// resources for v1alpha1 versioned OperatorVersions.
package v1alpha1

import (
	"encoding/json"

	"github.com/coreos/go-semver/semver"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GroupVersion is the version used in the Scheme for OperatorVersions.
const GroupVersion = "v1alpha1"

// NamedInstallStrategy represents the block of an OperatorVersion resource
// where the install strategy is specified.
type NamedInstallStrategy struct {
	StrategyName    string          `json:"strategy"`
	StrategySpecRaw json.RawMessage `json:"spec"`
}

// Kind, ApiVersion, Name, Namespace uniquely identify a requirement
type Requirements struct {
	Kind             string                 `json:"kind"`
	ApiVersion       string                 `json:"apiVersion"`
	Name             string                 `json:"name"`
	Namespace        string                 `json:"namespace"`
	SHA256           string                 `json:"sha256"`
	Optional         bool                   `json:"optional"`
	MatchExpressions []metav1.LabelSelector `json:"matchExpressions"`
}

// OperatorVersionSpec declarations tell the ALM how to install an operator
// that can manage apps for given version and AppType.
type OperatorVersionSpec struct {
	InstallStrategy NamedInstallStrategy `json:"install"`
	Version         semver.Version       `json:"version"`
	Maturity        string               `json:"maturity"`
	Requirements    []Requirements       `json:"requirements"`
	Permissions     []string             `json:"permissions"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// OperatorVersion is a Custom Resource of type `OperatorVersionSpec`.
type OperatorVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   OperatorVersionSpec `json:"spec"`
	Status metav1.Status       `json:"status"`
}

// OperatorVersionList represents a list of OperatorVersions.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OperatorVersionList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []OperatorVersion `json:"items"`
}
