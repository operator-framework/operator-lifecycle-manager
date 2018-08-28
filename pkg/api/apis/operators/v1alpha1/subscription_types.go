package v1alpha1

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	SubscriptionKind          = "Subscription"
	SubscriptionCRDAPIVersion = operators.GroupName + "/" + GroupVersion
)

// SubscriptionState tracks when updates are available, installing, or service is up to date
type SubscriptionState string

// TODO(alecmerdler): Remove these in favor of `SubscriptionConditionType`
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

// SubscriptionConditionType is a label for the condition of a Subscription at the current time.
type SubscriptionConditionType string

const (
	SubscriptionConditionTypeNone             SubscriptionConditionType = ""
	SubscriptionConditionTypeUpdgrade                                   = "UpgradeFailed"
	SubscriptionConditionTypeUpgradeAvailable                           = "UpgradeAvailable"
	SubscriptionConditionTypeUpgradePending                             = "UpgradePending"
	SubscriptionConditionTypeAtLatest                                   = "AtLatestKnown"
)

// SubscriptionCondition represents a piece of the overall status of a Subscription.
type SubscriptionCondition struct {
	Type               SubscriptionConditionType `json:"type,omitempty"`
	Status             corev1.ConditionStatus    `json:"status,omitempty"`
	LastUpdateTime     metav1.Time               `json:"lastUpdateTime,omitempty"`
	LastTransitionTime metav1.Time               `json:"lastTransitionTime,omitempty"`
	Reason             ConditionReason           `json:"reason,omitempty"`
	Message            string                    `json:"message,omitempty"`
}

// SubscriptionSpec defines an Application that can be installed
type SubscriptionSpec struct {
	CatalogSource          string   `json:"source"`
	CatalogSourceNamespace string   `json:"sourceNamespace"`
	Package                string   `json:"name"`
	Channel                string   `json:"channel,omitempty"`
	StartingCSV            string   `json:"startingCSV,omitempty"`
	InstallPlanApproval    Approval `json:"installPlanApproval,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +genclient
type Subscription struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   *SubscriptionSpec  `json:"spec"`
	Status SubscriptionStatus `json:"status"`
}

type SubscriptionStatus struct {
	CurrentCSV   string                `json:"currentCSV,omitempty"`
	InstalledCSV string                `json:"installedCSV, omitempty"`
	Install      *InstallPlanReference `json:"installplan,omitempty"`

	State  SubscriptionState `json:"state,omitempty"`
	Reason ConditionReason   `json:"reason,omitempty"`
	// +optional
	LastUpdateTime metav1.Time `json:"lastUpdateTime,omitempty"`
	// +optional
	LastTransitionTime metav1.Time             `json:"lastTransitionTime,omitempty"`
	Conditions         []SubscriptionCondition `json:"conditions,omitempty"`
}

type InstallPlanReference struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	UID        types.UID `json:"uuid"`
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

// SetCondition adds or updates a condition, using `Type` as merge key
func (s *SubscriptionStatus) SetCondition(cond SubscriptionCondition) SubscriptionCondition {
	updated := now()
	cond.LastUpdateTime = updated
	cond.LastTransitionTime = updated

	for i, existing := range s.Conditions {
		if existing.Type != cond.Type {
			continue
		}
		if existing.Status == cond.Status {
			cond.LastTransitionTime = existing.LastTransitionTime
		}
		s.Conditions[i] = cond
		return cond
	}
	s.Conditions = append(s.Conditions, cond)
	return cond
}

func SubscriptionConditionFailed(cond SubscriptionConditionType, reason ConditionReason, err error) SubscriptionCondition {
	return SubscriptionCondition{
		Type:    cond,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: err.Error(),
	}
}

func SubscriptionConditionMet(cond SubscriptionConditionType) SubscriptionCondition {
	return SubscriptionCondition{
		Type:   cond,
		Status: corev1.ConditionTrue,
	}
}
