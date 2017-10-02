package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	subv1 "github.com/coreos-inc/alm/apis/subscription/v1alpha1"
)

const (
	GroupVersion = "v1alpha1" // used in registering InstallPlan scheme

	InstallPlanCRDName       = "installplan-v1s.app.coreos.com"
	InstallPlanCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// InstallPlanSpec defines a set of Application resources to be installed
type InstallPlanSpec struct {
	ClusterServiceVersions string         `json:"ClusterServiceVersions"`
	Approval               subv1.Approval `json:"approval"`
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
