package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	InstallDeclarationCRDName       = "apptype-v1s.app.coreos.com"
	InstallDeclarationCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support
)

// InstallDeclarationSpec defines a set of Application resources to be installed
type InstallDeclarationSpec struct {
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "InstallDeclarationSpec"
type InstallDeclaration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *InstallDeclarationSpec `json:"spec"`
	Status metav1.Status           `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type InstallDeclarationList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*InstallDeclarationSpec `json:"items"`
}
