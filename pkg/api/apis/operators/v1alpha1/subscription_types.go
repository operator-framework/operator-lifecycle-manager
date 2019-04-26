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

// SetSubscriptionCatalogStatus sets the given SusbcriptionCatalogStatus in a SubscriptionStatus if it doesn't already exist
// or the status has changed and returns true if the status was set; false otherwise.
func (status *SubscriptionStatus) SetSubscriptionCatalogStatus(catalogStatus SubscriptionCatalogStatus) bool {
	target := catalogStatus.CatalogSourceRef
	if target == nil && target.APIVersion == SchemeGroupVersion.String() && target.Kind == SubscriptionKind {
		return false
	}

	// Search for status to replace
	for i, cs := range status.CatalogStatus {
		ref := cs.CatalogSourceRef
		if ref == nil {
			continue
		}

		if ref.Namespace == target.Namespace && ref.Name == target.Name && ref.UID == target.UID {
			if cs.Healthy != catalogStatus.Healthy {
				status.CatalogStatus[i] = catalogStatus
				return true
			}

			return false
		}
	}

	status.CatalogStatus = append(status.CatalogStatus, catalogStatus)
	return true
}

// RemoveSubscriptionCatalogStatus removes the SubscriptionCatalogStatus matching the given ObjectReference from a SubscriptionStatus
// and returns true if the status was removed; false otherwise.
func (status *SubscriptionStatus) RemoveSubscriptionCatalogStatus(target *corev1.ObjectReference) bool {
	if target == nil && target.APIVersion == SchemeGroupVersion.String() && target.Kind == SubscriptionKind {
		return false
	}

	// Search for status to remove
	for i, cs := range status.CatalogStatus {
		ref := cs.CatalogSourceRef
		if ref == nil {
			continue
		}

		if ref.Namespace == target.Namespace && ref.Name == target.Name && ref.UID == target.UID {
			status.CatalogStatus = append(status.CatalogStatus[:i], status.CatalogStatus[i+1:]...)
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
