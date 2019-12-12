package v1alpha1

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestSetRequirementStatus(t *testing.T) {
	csv := ClusterServiceVersion{}
	status := []RequirementStatus{{Group: "test", Version: "test", Kind: "Test", Name: "test", Status: "test", UUID: "test"}}
	csv.SetRequirementStatus(status)
	require.Equal(t, csv.Status.RequirementStatus, status)
}

func TestSetPhase(t *testing.T) {
	tests := []struct {
		currentPhase      ClusterServiceVersionPhase
		currentConditions []ClusterServiceVersionCondition
		inPhase           ClusterServiceVersionPhase
		outPhase          ClusterServiceVersionPhase
		description       string
	}{
		{
			currentPhase:      "",
			currentConditions: []ClusterServiceVersionCondition{},
			inPhase:           CSVPhasePending,
			outPhase:          CSVPhasePending,
			description:       "NoPhase",
		},
		{
			currentPhase:      CSVPhasePending,
			currentConditions: []ClusterServiceVersionCondition{{Phase: CSVPhasePending}},
			inPhase:           CSVPhasePending,
			outPhase:          CSVPhasePending,
			description:       "SamePhase",
		},
		{
			currentPhase:      CSVPhasePending,
			currentConditions: []ClusterServiceVersionCondition{{Phase: CSVPhasePending}},
			inPhase:           CSVPhaseInstalling,
			outPhase:          CSVPhaseInstalling,
			description:       "DifferentPhase",
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			csv := ClusterServiceVersion{
				Status: ClusterServiceVersionStatus{
					Phase:      tt.currentPhase,
					Conditions: tt.currentConditions,
				},
			}
			now := metav1.Now()
			csv.SetPhase(tt.inPhase, "test", "test", &now)
			require.EqualValues(t, tt.outPhase, csv.Status.Phase)
		})
	}
}

func TestIsObsolete(t *testing.T) {
	tests := []struct {
		currentPhase      ClusterServiceVersionPhase
		currentConditions []ClusterServiceVersionCondition
		out               bool
		description       string
	}{
		{
			currentPhase:      "",
			currentConditions: []ClusterServiceVersionCondition{},
			out:               false,
			description:       "NoPhase",
		},
		{
			currentPhase:      CSVPhasePending,
			currentConditions: []ClusterServiceVersionCondition{{Phase: CSVPhasePending}},
			out:               false,
			description:       "Pending",
		},
		{
			currentPhase:      CSVPhaseReplacing,
			currentConditions: []ClusterServiceVersionCondition{{Phase: CSVPhaseReplacing, Reason: CSVReasonBeingReplaced}},
			out:               true,
			description:       "Replacing",
		},
		{
			currentPhase:      CSVPhaseDeleting,
			currentConditions: []ClusterServiceVersionCondition{{Phase: CSVPhaseDeleting, Reason: CSVReasonReplaced}},
			out:               true,
			description:       "CSVPhaseDeleting",
		},
	}
	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			csv := ClusterServiceVersion{
				Status: ClusterServiceVersionStatus{
					Phase:      tt.currentPhase,
					Conditions: tt.currentConditions,
				},
			}
			require.Equal(t, csv.IsObsolete(), tt.out)
		})
	}
}

func TestSupports(t *testing.T) {
	tests := []struct {
		description       string
		installModeSet    InstallModeSet
		operatorNamespace string
		namespaces        []string
		expectedErr       error
	}{
		{
			description: "NoNamespaces",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    true,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{},
			expectedErr:       fmt.Errorf("operatorgroup has invalid selected namespaces, cannot configure to watch zero namespaces"),
		},
		{
			description: "OwnNamespace/OperatorNamespace/Supported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    true,
				InstallModeTypeSingleNamespace: false,
				InstallModeTypeMultiNamespace:  false,
				InstallModeTypeAllNamespaces:   false,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"operators"},
			expectedErr:       nil,
		},
		{
			description: "SingleNamespace/OtherNamespace/Supported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    false,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  false,
				InstallModeTypeAllNamespaces:   false,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0"},
			expectedErr:       nil,
		},
		{
			description: "MultiNamespace/OtherNamespaces/Supported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    false,
				InstallModeTypeSingleNamespace: false,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   false,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0", "ns-2"},
			expectedErr:       nil,
		},
		{
			description: "AllNamespaces/NamespaceAll/Supported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    false,
				InstallModeTypeSingleNamespace: false,
				InstallModeTypeMultiNamespace:  false,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{""},
			expectedErr:       nil,
		},
		{
			description: "OwnNamespace/OperatorNamespace/Unsupported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    false,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"operators"},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch own namespace", InstallModeTypeOwnNamespace),
		},
		{
			description: "OwnNamespace/IncludesOperatorNamespace/Unsupported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    false,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0", "operators"},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch own namespace", InstallModeTypeOwnNamespace),
		},
		{
			description: "MultiNamespace/OtherNamespaces/Unsupported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    true,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  false,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0", "ns-1"},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch 2 namespaces", InstallModeTypeMultiNamespace),
		},
		{
			description: "SingleNamespace/OtherNamespace/Unsupported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    true,
				InstallModeTypeSingleNamespace: false,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0"},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch one namespace", InstallModeTypeSingleNamespace),
		},
		{
			description: "AllNamespaces/NamespaceAll/Unsupported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    true,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   false,
			},
			operatorNamespace: "operators",
			namespaces:        []string{""},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch all namespaces", InstallModeTypeAllNamespaces),
		},
		{
			description: "AllNamespaces/IncludingNamespaceAll/Unsupported",
			installModeSet: InstallModeSet{
				InstallModeTypeOwnNamespace:    true,
				InstallModeTypeSingleNamespace: true,
				InstallModeTypeMultiNamespace:  true,
				InstallModeTypeAllNamespaces:   true,
			},
			operatorNamespace: "operators",
			namespaces:        []string{"", "ns-0"},
			expectedErr:       fmt.Errorf("operatorgroup has invalid selected namespaces, NamespaceAll found when |selected namespaces| > 1"),
		},
		{
			description:       "NoNamespaces/EmptyInstallModeSet/Unsupported",
			installModeSet:    InstallModeSet{},
			operatorNamespace: "",
			namespaces:        []string{},
			expectedErr:       fmt.Errorf("operatorgroup has invalid selected namespaces, cannot configure to watch zero namespaces"),
		},
		{
			description:       "MultiNamespace/OtherNamespaces/EmptyInstallModeSet/Unsupported",
			installModeSet:    InstallModeSet{},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0", "ns-1"},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch 2 namespaces", InstallModeTypeMultiNamespace),
		},
		{
			description:       "SingleNamespace/OtherNamespace/EmptyInstallModeSet/Unsupported",
			installModeSet:    InstallModeSet{},
			operatorNamespace: "operators",
			namespaces:        []string{"ns-0"},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch one namespace", InstallModeTypeSingleNamespace),
		},
		{
			description:       "AllNamespaces/NamespaceAll/EmptyInstallModeSet/Unsupported",
			installModeSet:    InstallModeSet{},
			operatorNamespace: "operators",
			namespaces:        []string{corev1.NamespaceAll},
			expectedErr:       fmt.Errorf("%s InstallModeType not supported, cannot configure to watch all namespaces", InstallModeTypeAllNamespaces),
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			err := tt.installModeSet.Supports(tt.operatorNamespace, tt.namespaces)
			require.Equal(t, tt.expectedErr, err)
		})
	}
}

func TestSetPhaseWithConditions(t *testing.T) {
	tests := []struct {
		description   string
		limit         int
		currentLength int
		startIndex    int
	}{
		{
			// The original list is already at limit (length == limit).
			// We expect the oldest element ( item at 0 index) to be removed.
			description:   "TestSetPhaseWithConditionsLengthAtLimit",
			limit:         ConditionsLengthLimit,
			currentLength: ConditionsLengthLimit,

			// The first element from the original list should be dropped from
			// the new list.
			startIndex: 1,
		},
		{
			// The original list is 1 length away from limit.
			// We don't expect the list to be trimmed.
			description:   "TestSetPhaseWithConditionsLengthBelowLimit",
			limit:         ConditionsLengthLimit,
			currentLength: ConditionsLengthLimit - 1,

			// Everything in the original list should be preserved.
			startIndex: 0,
		},
		{
			// The original list has N more element(s) than allowed limit.
			// We expect (N + 1) oldest elements to be deleted to keep the list
			// at limit.
			description:   "TestSetPhaseWithConditionsLimitExceeded",
			limit:         ConditionsLengthLimit,
			currentLength: ConditionsLengthLimit + 10,

			// The first 11 (N=10 plus 1 to make room for the newly added
			// condition) elements from the original list should be dropped.
			startIndex: 11,
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			csv := ClusterServiceVersion{}
			csv.Status.Conditions = helperNewConditions(tt.currentLength)

			now := metav1.Now()

			oldConditionsWant := csv.Status.Conditions[tt.startIndex:]
			lastAddedConditionWant := ClusterServiceVersionCondition{
				Phase:              ClusterServiceVersionPhase("Pending"),
				LastTransitionTime: &now,
				LastUpdateTime:     &now,
				Message:            "message",
				Reason:             ConditionReason("reason"),
			}

			csv.SetPhase("Pending", "reason", "message", &now)

			conditionsGot := csv.Status.Conditions
			assert.Equal(t, tt.limit, len(conditionsGot))

			oldConditionsGot := conditionsGot[0 : len(conditionsGot)-1]
			assert.EqualValues(t, oldConditionsWant, oldConditionsGot)

			lastAddedConditionGot := conditionsGot[len(conditionsGot)-1]
			assert.Equal(t, lastAddedConditionWant, lastAddedConditionGot)
		})
	}
}

func helperNewConditions(count int) []ClusterServiceVersionCondition {
	conditions := make([]ClusterServiceVersionCondition, 0)

	for i := 1; i <= count; i++ {
		now := metav1.Now()
		condition := ClusterServiceVersionCondition{
			Phase:              ClusterServiceVersionPhase(fmt.Sprintf("phase-%d", i)),
			LastTransitionTime: &now,
			LastUpdateTime:     &now,
			Message:            fmt.Sprintf("message-%d", i),
			Reason:             ConditionReason(fmt.Sprintf("reason-%d", i)),
		}
		conditions = append(conditions, condition)
	}

	return conditions
}
