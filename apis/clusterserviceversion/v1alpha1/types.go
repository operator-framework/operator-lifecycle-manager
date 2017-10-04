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

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ClusterServiceVersion is a Custom Resource of type `ClusterServiceVersionSpec`.
type ClusterServiceVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   ClusterServiceVersionSpec `json:"spec"`
	Status metav1.Status             `json:"status"`
}

// ClusterServiceVersionList represents a list of ClusterServiceVersions.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterServiceVersionList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []ClusterServiceVersion `json:"items"`
}
