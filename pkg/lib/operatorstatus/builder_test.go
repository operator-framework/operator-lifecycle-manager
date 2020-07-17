package operatorstatus

import (
	"testing"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/clock"

	"github.com/stretchr/testify/assert"
)

func TestBuilder(t *testing.T) {
	fakeClock := clock.NewFakeClock(time.Now())
	minuteAgo := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	tests := []struct {
		name     string
		action   func(b *Builder)
		existing *configv1.ClusterOperatorStatus
		expected *configv1.ClusterOperatorStatus
	}{
		// Condition: (Progressing, True).
		// existing status.Conditions is empty.
		{
			name: "WithProgressing/NoProgressingConditionPresentInExistingStatus",
			action: func(b *Builder) {
				b.WithProgressing(configv1.ConditionTrue, "message")
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					configv1.ClusterOperatorStatusCondition{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						Message:            "message",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// Condition: (Progressing, True).
		// (Progressing, False) is already present in existing status.Conditions.
		{
			name: "WithProgressing/ProgressingConditionPresentInExistingStatus",
			action: func(b *Builder) {
				b.WithProgressing(configv1.ConditionTrue, "message")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					configv1.ClusterOperatorStatusCondition{
						Type:   configv1.OperatorProgressing,
						Status: configv1.ConditionFalse,
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					configv1.ClusterOperatorStatusCondition{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						Message:            "message",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// Condition: (Progressing, True).
		// (Progressing, True) is already present in existing status.Conditions.
		{
			name: "WithProgressing/ProgressingConditionMatchesInExistingStatus",
			action: func(b *Builder) {
				b.WithProgressing(configv1.ConditionTrue, "message")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					configv1.ClusterOperatorStatusCondition{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						LastTransitionTime: minuteAgo,
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					configv1.ClusterOperatorStatusCondition{
						Type:               configv1.OperatorProgressing,
						Status:             configv1.ConditionTrue,
						Message:            "message",
						LastTransitionTime: minuteAgo,
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// Condition: (Upgradeable, True).
		// existing status.Conditions is empty.
		{
			name: "WithUpgradeable/NoUpgradeableConditionPresentInExistingStatus",
			action: func(b *Builder) {
				b.WithUpgradeable(configv1.ConditionTrue, "message")
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "message",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// Condition: (Upgradeable, True).
		// (Upgradeable, False) is already present in existing status.Conditions.
		{
			name: "WithUpgradeable/UpgradeableConditionPresentInExistingStatus",
			action: func(b *Builder) {
				b.WithUpgradeable(configv1.ConditionTrue, "message")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:   configv1.OperatorUpgradeable,
						Status: configv1.ConditionFalse,
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "message",
						LastTransitionTime: metav1.NewTime(fakeClock.Now()),
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// Condition: (Upgradeable, True).
		// (Upgradeable, True) is already present in existing status.Conditions.
		{
			name: "WithUpgradeable/UpgradeableConditionMatchesInExistingStatus",
			action: func(b *Builder) {
				b.WithUpgradeable(configv1.ConditionTrue, "message")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						LastTransitionTime: minuteAgo,
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:               configv1.OperatorUpgradeable,
						Status:             configv1.ConditionTrue,
						Message:            "message",
						LastTransitionTime: minuteAgo,
					},
				},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// A new version is being added to status.
		// Existing status does not have any matching name.
		{
			name: "WithVersion/WithNoMatchingName",
			action: func(b *Builder) {
				b.WithVersion("foo", "1.00")
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// A new version is being added to status.
		// Existing status already has a matching same name and version.
		{
			name: "WithVersion/WithMatchingNameAndVersion",
			action: func(b *Builder) {
				b.WithVersion("foo", "1.00")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// A new version is being added to status.
		// Existing status already has a matching name but a different version.
		{
			name: "WithVersion/WithMatchingNameButDifferentVersion",
			action: func(b *Builder) {
				b.WithVersion("foo", "2.00")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "2.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// Multiple versions are added to status.
		{
			name: "WithVersion/WithMultipleVersions",
			action: func(b *Builder) {
				b.WithVersion("foo", "2.00").WithVersion("bar", "1.00")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "2.00",
					},
					configv1.OperandVersion{
						Name:    "bar",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// A version is being removed from status.
		// Existing status already has a matching name.
		{
			name: "WithoutVersion/WithMatchingName",
			action: func(b *Builder) {
				b.WithoutVersion("foo", "1.00")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "foo",
						Version: "1.00",
					},
					configv1.OperandVersion{
						Name:    "bar",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions: []configv1.OperandVersion{
					configv1.OperandVersion{
						Name:    "bar",
						Version: "1.00",
					},
				},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},

		// A new related object is being added.
		// Existing status is empty.
		{
			name: "WithRelatedObject/ReferenceNotPresentInStatus",
			action: func(b *Builder) {
				b.WithRelatedObject("group", "resources", "namespace", "name")
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions:   []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{
					configv1.ObjectReference{
						Group:     "group",
						Resource:  "resources",
						Namespace: "namespace",
						Name:      "name",
					},
				},
			},
		},

		// A new related object reference is being added.
		// Existing status already has the same related object reference.
		{
			name: "WithRelatedObject/ReferencePresentInStatus",
			action: func(b *Builder) {
				b.WithRelatedObject("group", "resources", "namespace", "name")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions:   []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{
					configv1.ObjectReference{
						Group:     "group",
						Resource:  "resources",
						Namespace: "namespace",
						Name:      "name",
					},
				},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions:   []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{
					configv1.ObjectReference{
						Group:     "group",
						Resource:  "resources",
						Namespace: "namespace",
						Name:      "name",
					},
				},
			},
		},

		// A related object reference is being removed.
		// Existing status already has the same related object reference.
		{
			name: "WithoutRelatedObject/ReferenceBeingRemoved",
			action: func(b *Builder) {
				b.WithoutRelatedObject("group", "resources", "namespace", "name")
			},
			existing: &configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{},
				Versions:   []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{
					configv1.ObjectReference{
						Group:     "group",
						Resource:  "resources",
						Namespace: "namespace",
						Name:      "name",
					},
				},
			},
			expected: &configv1.ClusterOperatorStatus{
				Conditions:     []configv1.ClusterOperatorStatusCondition{},
				Versions:       []configv1.OperandVersion{},
				RelatedObjects: []configv1.ObjectReference{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := &Builder{
				clock:  fakeClock,
				status: tt.existing,
			}

			// Go through the build steps specified.
			tt.action(builder)

			statusWant := tt.expected
			statusGot := builder.GetStatus()

			assert.Equal(t, statusWant, statusGot)
		})
	}
}
