package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	GroupVersion = "v1alpha1" // used in registering InstallPlan scheme

	InstallPlanCRDName       = "apptype-v1s.app.coreos.com"
	InstallPlanCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// InstallPlanSpec defines a set of Application resources to be installed
type InstallPlanSpec struct {
	DesiredClusterServiceVersion `json:"desiredClusterServiceVersion"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "InstallPlanSpec"
type InstallPlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *InstallPlanSpec `json:"spec"`
	Status metav1.Status    `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type InstallPlanList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*InstallPlanSpec `json:"items"`
}
