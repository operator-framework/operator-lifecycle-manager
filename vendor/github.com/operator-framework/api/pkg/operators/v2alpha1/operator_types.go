/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v2alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OperatorSpec defines the desired state of Operator
type OperatorSpec struct{}

// OperatorStatus describes the observed state of an operator and its components.
type OperatorStatus struct {
	// Components describes resources that compose the operator.
	// +optional
	Components *Components `json:"components,omitempty"`
}

// ConditionType codifies a condition's type.
type ConditionType string

// Condition represent the latest available observations of an component's state.
type Condition struct {
	// Type of condition.
	Type ConditionType `json:"type"`
	// Status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus `json:"status"`
	// The reason for the condition's last transition.
	// +optional
	Reason string `json:"reason,omitempty"`
	// A human readable message indicating details about the transition.
	// +optional
	Message string `json:"message,omitempty"`
	// Last time the condition was probed
	// +optional
	LastUpdateTime *metav1.Time `json:"lastUpdateTime,omitempty"`
	// Last time the condition transitioned from one status to another.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

// Components tracks the resources that compose an operator.
type Components struct {
	// LabelSelector is a label query over a set of resources used to select the operator's components
	LabelSelector *metav1.LabelSelector `json:"labelSelector"`
	// Refs are a set of references to the operator's component resources, selected with LabelSelector.
	// +optional
	Refs []RichReference `json:"refs,omitempty"`
}

// RichReference is a reference to a resource, enriched with its status conditions.
type RichReference struct {
	*corev1.ObjectReference `json:",inline"`
	// Conditions represents the latest state of the component.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:storageversion
// +kubebuilder:resource:categories=olm,scope=Cluster
// +kubebuilder:subresource:status

// Operator represents a cluster operator.
type Operator struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OperatorSpec   `json:"spec,omitempty"`
	Status OperatorStatus `json:"status,omitempty"`
}

// +genclient:nonNamespaced
// +kubebuilder:object:root=true

// OperatorList contains a list of Operators.
type OperatorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Operator `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Operator{}, &OperatorList{})
}
