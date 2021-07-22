package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

// AvailableClusterServiceVersionList is a list of AvailableClusterServiceVersion objects.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AvailableClusterServiceVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	// +listType=set
	Items []AvailableClusterServiceVersion `json:"items"`
}

// AvailableClusterServiceVersion indicates that an operator (CSV) of the same spec is available for use in this ns.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AvailableClusterServiceVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AvailableClusterServiceVersionSpec   `json:"spec,omitempty"`
	Status AvailableClusterServiceVersionStatus `json:"status,omitempty"`
}

// AvailableClusterServiceVersionSpec matches the spec of the csv
type AvailableClusterServiceVersionSpec struct {
	operatorv1alpha1.ClusterServiceVersionSpec `json:",inline"`
}

// AvailableClusterServiceVersionStatus matches the status of the csv
type AvailableClusterServiceVersionStatus struct {
	operatorv1alpha1.ClusterServiceVersionStatus `json:",inline"`
}
