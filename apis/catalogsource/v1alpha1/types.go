package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	CatalogSourceCRDName       = "apptype-v1s.app.coreos.com"
	CatalogSourceCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support

	SourceTypeInMem       = "in-mem"
	SourceTypeConfigMap   = "configmap"
	SourceTypeOmaha       = "omaha"
	SourceTypeAppRegistry = "registry"
)

// CatalogSourceSpec defines a remote (or in-cluster) source for application packages
type CatalogSourceSpec struct {
	// Identifying information
	SourceType string `json:"sourceType"` // only SourceTypeInMem or SourceTypeConfigMap supported
	Name       string `json:"name"`       // short name, should be unique to source

	// Display information - used in console on catalog source page
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	SourceURL   string `json:"source"`   // e.g. quay.io/applications/coreos, github.com/k8s/charts
	Provider    string `json:"provider"` // catalog publishers, e.g. "Salesforce", "BigCo Internal"
	Icon        Icon   `json:"icon"`

	// SourceType-specific configuration

	// SourceTypeConfigMap specific fields
	ConfigMaps []CatalogConfigMaps `json:"configMaps,omitempty"`
}

type Icon struct {
	Data      string `json:"base64data"`
	MediaType string `json:"mediatype"`
}

type CatalogConfigMaps struct {
	LabelSelectors []metav1.LabelSelector `json:"labelSelectors"`
	Namespace      string                 `json:"namespace"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "CatalogSourceSpec"
type CatalogSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *CatalogSourceSpec `json:"spec"`
	Status metav1.Status      `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type CatalogSourceList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*CatalogSourceSpec `json:"items"`
}
