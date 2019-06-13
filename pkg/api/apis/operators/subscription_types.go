package operators

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// SubscriptionKind is the PascalCase name of a Subscription's kind.
const SubscriptionKind = "Subscription"

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
	CatalogSource          string
	CatalogSourceNamespace string
	Package                string
	Channel                string
	StartingCSV            string
	InstallPlanApproval    Approval
}

// SubscriptionConditionType indicates an explicit state condition about a Subscription in "abnormal-true"
// polarity form (see https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties).
type SubscriptionConditionType string

const (
	// SubscriptionCatalogSourcesUnhealthy indicates that some or all of the CatalogSources to be used in resolution are unhealthy.
	SubscriptionCatalogSourcesUnhealthy SubscriptionConditionType = "CatalogSourcesUnhealthy"
)

const (
	// NoCatalogSourcesFound is a reason string for Subscriptions with unhealthy CatalogSources due to none being available.
	NoCatalogSourcesFound = "NoCatalogSourcesFound"

	// AllCatalogSourcesHealthy is a reason string for Subscriptions whose CatalogSources are all healthy.
	AllCatalogSourcesHealthy = "AllCatalogSourcesHealthy"

	// CatalogSourcesAdded is a reason string for Subscriptions that transitioned due to CatalogSources being added.
	CatalogSourcesAdded = "CatalogSourcesAdded"

	// CatalogSourcesUpdated is a reason string for Subscriptions that transitioned due to CatalogSource being updated..
	CatalogSourcesUpdated = "CatalogSourcesUpdated"

	// CatalogSourcesDeleted is a reason string for Subscriptions that transitioned due to CatalogSources being removed.
	CatalogSourcesDeleted = "CatalogSourcesDeleted"

	// UnhealthyCatalogSourceFound is a reason string for Subscriptions that transitioned because an unhealthy CatalogSource was found.
	UnhealthyCatalogSourceFound = "UnhealthyCatalogSourceFound"
)

type SubscriptionCondition struct {
	// Type is the type of Subscription condition.
	Type SubscriptionConditionType

	// Status is the status of the condition, one of True, False, Unknown.
	Status corev1.ConditionStatus

	// Reason is a one-word CamelCase reason for the condition's last transition.
	// +optional
	Reason string

	// Message is a human-readable message indicating details about last transition.
	// +optional
	Message string

	// LastHeartbeatTime is the last time we got an update on a given condition
	// +optional
	LastHeartbeatTime *metav1.Time

	// LastTransitionTime is the last time the condition transit from one status to another
	// +optional
	LastTransitionTime *metav1.Time
}

// Equals returns true if a SubscriptionCondition equals the one given, false otherwise.
// Equality is determined by the equality of the type, status, reason, and message fields ONLY.
func (s SubscriptionCondition) Equals(condition SubscriptionCondition) bool {
	return s.Type == condition.Type && s.Status == condition.Status && s.Reason == condition.Reason && s.Message == condition.Message
}

type SubscriptionStatus struct {
	// CurrentCSV is the CSV the Subscription is progressing to.
	// +optional
	CurrentCSV string

	// InstalledCSV is the CSV currently installed by the Subscription.
	// +optional
	InstalledCSV string

	// Install is a reference to the latest InstallPlan generated for the Subscription.
	// DEPRECATED: InstallPlanRef
	// +optional
	Install *InstallPlanReference

	// State represents the current state of the Subscription
	// +optional
	State SubscriptionState

	// Reason is the reason the Subscription was transitioned to its current state.
	// +optional
	Reason ConditionReason

	// InstallPlanRef is a reference to the latest InstallPlan that contains the Subscription's current CSV.
	// +optional
	InstallPlanRef *corev1.ObjectReference

	// CatalogHealth contains the Subscription's view of its relevant CatalogSources' status.
	// It is used to determine SubscriptionStatusConditions related to CatalogSources.
	// +optional
	CatalogHealth []SubscriptionCatalogHealth

	// Conditions is a list of the latest available observations about a Subscription's current state.
	// +optional
	Conditions []SubscriptionCondition

	// LastUpdated represents the last time that the Subscription status was updated.
	LastUpdated metav1.Time
}

// GetCondition returns the SubscriptionCondition of the given type if it exists in the SubscriptionStatus' Conditions.
// Returns a condition of the given type with a ConditionStatus of "Unknown" if not found.
func (status SubscriptionStatus) GetCondition(conditionType SubscriptionConditionType) SubscriptionCondition {
	for _, cond := range status.Conditions {
		if cond.Type == conditionType {
			return cond
		}
	}

	return SubscriptionCondition{
		Type:   conditionType,
		Status: corev1.ConditionUnknown,
	}
}

// SetCondition sets the given SubscriptionCondition in the SubscriptionStatus' Conditions.
func (status *SubscriptionStatus) SetCondition(condition SubscriptionCondition) {
	for i, cond := range status.Conditions {
		if cond.Type == condition.Type {
			status.Conditions[i] = condition
			return
		}
	}

	status.Conditions = append(status.Conditions, condition)
}

type InstallPlanReference struct {
	APIVersion string
	Kind       string
	Name       string
	UID        types.UID
}

// SubscriptionCatalogHealth describes the health of a CatalogSource the Subscription knows about.
type SubscriptionCatalogHealth struct {
	// CatalogSourceRef is a reference to a CatalogSource.
	CatalogSourceRef *corev1.ObjectReference

	// LastUpdated represents the last time that the CatalogSourceHealth changed
	LastUpdated *metav1.Time

	// Healthy is true if the CatalogSource is healthy; false otherwise.
	Healthy bool
}

// Equals returns true if a SubscriptionCatalogHealth equals the one given, false otherwise.
// Equality is based SOLEY on health and UID.
func (s SubscriptionCatalogHealth) Equals(health SubscriptionCatalogHealth) bool {
	return s.Healthy == health.Healthy && s.CatalogSourceRef.UID == health.CatalogSourceRef.UID
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient

// Subscription keeps operators up to date by tracking changes to Catalogs.
type Subscription struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   *SubscriptionSpec
	Status SubscriptionStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SubscriptionList is a list of Subscription resources.
type SubscriptionList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []Subscription
}

// GetInstallPlanApproval gets the configured install plan approval or the default
func (s *Subscription) GetInstallPlanApproval() Approval {
	if s.Spec.InstallPlanApproval == ApprovalManual {
		return ApprovalManual
	}
	return ApprovalAutomatic
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
