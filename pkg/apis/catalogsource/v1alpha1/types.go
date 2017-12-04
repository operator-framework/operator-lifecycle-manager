package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	CatalogSourceCRDName       = "catalogsource-v1s"
	CatalogSourceCRDAPIVersion = "app.coreos.com/v1alpha1" // API version w/ CRD support
	CatalogSourceKind          = "CatalogSource-v1"
	CatalogSourceListKind      = "CatalogSourceList-v1"
	GroupVersion               = "v1alpha1"
)

type CatalogSourceSpec struct {
	Name       string   `json:"name"`
	SourceType string   `json:"sourceType"`
	ConfigMap  string   `json:"configMap,omitempty"`
	Secrets    []string `json:"secrets,omitempty"`

	// Metadata
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Icon        Icon   `json:"icon,omitempty"`
}

type Icon struct {
	MediaType string `json:"mediatype"`
	Data      string `json:"base64data"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CatalogSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec CatalogSourceSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CatalogSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []CatalogSource `json:"items"`
}
