package v2alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OperatorSpec is the specification of an operator.
type OperatorSpec struct{}

// OperatorStatus describes the observed state of an operator and its components.
type OperatorStatus struct {
	// Components describes resources that compose the operator.
	// +optional
	Components *Components `json:"components,omitempty"`
}

// Components tracks the resources that compose an operator.
type Components struct {
	// LabelSelector is a label query over a set of resources used to select the operator's components
	LabelSelector *metav1.LabelSelector `json:"labelSelector"`

	// Refs are a set of references to the operator's component resources, selected with LabelSelector.
	// +optional
	Refs []Ref `json:"refs,omitempty"`
}

// Ref is a resource reference.
type Ref struct {
	*corev1.ObjectReference `json:",inline"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:storageversion

// Operator represents an operator on the cluster.
type Operator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OperatorSpec   `json:"spec,omitempty"`
	Status OperatorStatus `json:"status,omitempty"`
}

// +genclient:nonNamespaced
// +kubebuilder:object:root=true

// OperatorList is a list of operator resources.
type OperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Operator `json:"items"`
}
