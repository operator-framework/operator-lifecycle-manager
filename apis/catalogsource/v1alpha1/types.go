package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	CatalogSourceCRDName       = "apptype-v1s.app.coreos.com"
	CatalogSourceCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support

	SourceTypeOmaha       = "omaha"
	SourceTypeAppRegistry = "registry"
)

// CatalogSourceSpec defines a remote (or in-cluster) source for application packages
type CatalogSourceSpec struct {
	SourceType string `json:"sourceType"`
	URL        string `json:"url"`
	Name       string `json:"name"`
	SecretName string `json:"secretName"`
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
