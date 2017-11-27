package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type CatalogSourceSpec struct {
	Name       string   `json:"name"`
	SourceType string   `json:"sourceType"`
	URL        string   `json:"url,omitempty"`
	ConfigMap  string   `json:"configMap,omitempty"`
	Secrets    []string `json:"secrets,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CatalogSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec CatalogSourceSpec `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type CatalogSourceList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []CatalogSource `json:"items"`
}
