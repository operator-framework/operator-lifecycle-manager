package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type CatalogSourceSpec struct {
	SourceType string   `json:"sourceType"`
	URL        string   `json:"url"`
	ConfigMap  string   `json:"configMap"`
	Name       string   `json:"name"`
	Secrets    []string `json:"secrets"`
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
