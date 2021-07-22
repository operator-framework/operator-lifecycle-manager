package available

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	operatorv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
)

// AvailableClusterServiceVersionList is a list of AvailableClusterServiceVersion objects.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AvailableClusterServiceVersionList struct {
	metav1.TypeMeta
	metav1.ListMeta
	// +listType=set
	Items []AvailableClusterServiceVersion
}

// AvailableClusterServiceVersion indicates that an operator (CSV) of the same spec is available for use in this ns.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AvailableClusterServiceVersion struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   AvailableClusterServiceVersionSpec
	Status AvailableClusterServiceVersionStatus
}

// AvailableClusterServiceVersionSpec matches the spec of the csv
type AvailableClusterServiceVersionSpec struct {
	operatorv1alpha1.ClusterServiceVersionSpec
}

// AvailableClusterServiceVersionStatus matches the status of the csv
type AvailableClusterServiceVersionStatus struct {
	operatorv1alpha1.ClusterServiceVersionStatus
}
