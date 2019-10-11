package v2alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// Operator represents an operator on the cluster.
type Operator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   OperatorSpec   `json:"spec"`
	Status OperatorStatus `json:"status"`
}

// OperatorSpec is the specification of an operator.
type OperatorSpec struct{}

// OperatorStatus describes the observed state of an operator and its components.
type OperatorStatus struct {
	// Components describes resources that compose the operator.
	Components *Components `json:"components,omitempty"`
}

// Components tracks the resources that compose an operator.
type Components struct {
	// labelSelector is a label query over a set of resources used to select the operator's components
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
	// Refs are a set of references to the operator's component resources, selected with LabelSelector.
	Refs []Ref `json:"refs,omitempty"`
}

// Ref is a resource reference.
type Ref struct {
	*corev1.ObjectReference `json:",inline"`
}

// +kubebuilder:object:root=true

// OperatorList is a list of operator resources.
type OperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Operator `json:"items"`
}
