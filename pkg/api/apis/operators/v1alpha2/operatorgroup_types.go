package v1alpha2

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type OperatorGroupSpec struct {
	Selector       metav1.LabelSelector  `json:"selector,omitempty"`
	ServiceAccount corev1.ServiceAccount `json:"serviceAccount,omitempty"`
}

type OperatorGroupStatus struct {
	Namespaces  []*corev1.Namespace `json:"namespaces"`
	LastUpdated metav1.Time         `json:"lastUpdated"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
type OperatorGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   OperatorGroupSpec   `json:"spec"`
	Status OperatorGroupStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OperatorGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []OperatorGroup `json:"items"`
}
