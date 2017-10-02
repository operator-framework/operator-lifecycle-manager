package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const (
	GroupVersion = "v1alpha1" // GroupVersion is the version used in the Scheme for subscriptions

	SubscriptionCRDName       = "apptype-v1s.app.coreos.com"
	SubscriptionCRDAPIVersion = "apiextensions.k8s.io/v1beta1" // API version w/ CRD support

	ApprovalAutomatic  Approval = "Automatic"
	ApprovalUpdateOnly Approval = "Update-Only"
	ApprovalManual     Approval = "Manual"
)

type Approval string

// SubscriptionSpec defines an Application that can be installed
type SubscriptionSpec struct {
	Source   string   `json:"source"`
	AppType  string   `json:"apptype"`
	Channel  string   `json:"channel"`
	Approval Approval `json:"approval"`
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
