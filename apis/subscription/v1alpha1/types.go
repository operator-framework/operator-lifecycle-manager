package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	SubscriptionCRDName       = "apptype-v1s.app.coreos.com"
	SubscriptionCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support

	ApprovalAutomatic  = "automatic"
	ApprovalUpdateOnly = "update-only"
	ApprovalManual     = "manual"
)

// SubscriptionSpec defines an Application that can be installed
type SubscriptionSpec struct {
	Source   string `json:"source"`
	AppType  string `json:"apptype"`
	Channel  string `json:"channel"`
	Approval string `json:"approval"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Custom Resource of type "SubscriptionSpec"
type Subscription struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *SubscriptionSpec  `json:"spec"`
	Status SubscriptionStatus `json:"status"`
}

type SubscriptionStatus struct {
	metav1.Status `json:",inline"`

	CurrentVersion string `json:"currentVersion"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SubscriptionList struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Items []*SubscriptionSpec `json:"items"`
}
