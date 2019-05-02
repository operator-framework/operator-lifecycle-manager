package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	SubscriptionKind          = "Subscription"
	SubscriptionCRDAPIVersion = GroupName + "/" + GroupVersion
)

// SubscriptionState tracks when updates are available, installing, or service is up to date
type SubscriptionState string

const (
	SubscriptionStateNone             = ""
	SubscriptionStateFailed           = "UpgradeFailed"
	SubscriptionStateUpgradeAvailable = "UpgradeAvailable"
	SubscriptionStateUpgradePending   = "UpgradePending"
	SubscriptionStateAtLatest         = "AtLatestKnown"
)

const (
	SubscriptionReasonInvalidCatalog   ConditionReason = "InvalidCatalog"
	SubscriptionReasonUpgradeSucceeded ConditionReason = "UpgradeSucceeded"
)

// SubscriptionSpec defines an Application that can be installed
type SubscriptionSpec struct {
	CatalogSource          string   `json:"source"`
	CatalogSourceNamespace string   `json:"sourceNamespace"`
	Package                string   `json:"name"`
	Channel                string   `json:"channel,omitempty"`
	StartingCSV            string   `json:"startingCSV,omitempty"`
	InstallPlanApproval    Approval `json:"installPlanApproval,omitempty"`
}

type SubscriptionStatus struct {
	// CurrentCSV is the CSV the Subscription is progressing to.
	// +optional
	CurrentCSV string `json:"currentCSV,omitempty"`

	// InstalledCSV is the CSV currently installed by the Subscription.
	// +optional
	InstalledCSV string `json:"installedCSV,omitempty"`

	// Install is a reference to the latest InstallPlan generated for the Subscription.
	// DEPRECATED: InstallPlanRef
	// +optional
	Install *InstallPlanReference `json:"installplan,omitempty"`

	// State represents the current state of the Subscription
	// +optional
	State SubscriptionState `json:"state,omitempty"`

	// Reason is the reason the Subscription was transitioned to its current state.
	// +optional
	Reason ConditionReason `json:"reason,omitempty"`

	// InstallPlanRef is a reference to the latest InstallPlan that contains the Subscription's current CSV.
	// +optional
	InstallPlanRef *corev1.ObjectReference `json:"installPlanRef,omitempty"`

	// CatalogStatus contains the Subscription's view of its relevant CatalogSources' status.
	// It is used to determine SubscriptionStatusConditions related to CatalogSources.
	// +optional
	CatalogStatus []SubscriptionCatalogStatus `json:"catalogStatus,omitempty"`

	// LastUpdated represents the last time that the Subscription status was updated.
	LastUpdated metav1.Time `json:"lastUpdated"`
}

type InstallPlanReference struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	UID        types.UID `json:"uuid"`
}

// NewInstallPlanReference returns an InstallPlanReference for the given ObjectReference.
func NewInstallPlanReference(ref *corev1.ObjectReference) *InstallPlanReference {
	return &InstallPlanReference{
		APIVersion: ref.APIVersion,
		Kind:       ref.Kind,
		Name:       ref.Name,
		UID:        ref.UID,
	}
}

// SubscriptionCatalogStatus describes a Subscription's view of a CatalogSource's status.
type SubscriptionCatalogStatus struct {
	// CatalogSourceRef is a reference to a CatalogSource.
	CatalogSourceRef *corev1.ObjectReference `json:"catalogSourceRef"`

	// LastUpdated represents the last time that the CatalogSourceHealth changed
	LastUpdated metav1.Time `json:"lastUpdated"`

	// Healthy is true if the CatalogSource is healthy; false otherwise.
	Healthy bool `json:"healthy"`
}

// SetSubscriptionCatalogStatus sets the SubscriptionStatus' CatalogStatus field as the given slice if it differs
// from the stored value. Returns true if a change was made, false otherwise.
func (status *SubscriptionStatus) SetSubscriptionCatalogStatus(catalogStatus []SubscriptionCatalogStatus) bool {
	if len(status.CatalogStatus) != len(catalogStatus) {
		status.CatalogStatus = catalogStatus
		return true
	}

	// TODO: dedupe catalogStatus?

	set := map[SubscriptionCatalogStatus]struct{}{}
	for _, cs := range status.CatalogStatus {
		set[cs] = struct{}{}
	}
	for _, cs := range catalogStatus {
		if _, ok := set[cs]; !ok {
			status.CatalogStatus = catalogStatus
			return true
		}
	}

	return false
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
type Subscription struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *SubscriptionSpec  `json:"spec"`
	Status SubscriptionStatus `json:"status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SubscriptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Subscription `json:"items"`
}

// GetInstallPlanApproval gets the configured install plan approval or the default
func (s *Subscription) GetInstallPlanApproval() Approval {
	if s.Spec.InstallPlanApproval == ApprovalManual {
		return ApprovalManual
	}
	return ApprovalAutomatic
}
