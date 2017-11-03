package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
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
			csv.SetPhase(tt.inPhase, "test", "test")
			require.EqualValues(t, tt.outPhase, csv.Status.Phase)
		})
	}
}
