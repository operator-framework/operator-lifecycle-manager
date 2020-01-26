package v1alpha1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOrderSteps(t *testing.T) {
	tests := []struct {
		description string
		in          []*Step
	}{
		{
			description: "EmptyList",
			in:          []*Step{},
		},
		{
			description: "csvsRDS",
			in:          []*Step{step(crdKind), step(ClusterServiceVersionKind), step(crdKind), step(crdKind)},
		},
		{
			description: "csvsCRDSAndRandomKinds",
			in:          []*Step{step(crdKind), step(ClusterServiceVersionKind), step(crdKind), step(crdKind), step("These"), step("are"), step("random"), step("Kinds")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := OrderSteps(tt.in)
			require.EqualValues(t, len(result), len(tt.in))
			require.True(t, isOrdered(result))
		})
	}
}

func step(kind string) *Step {
	resource := StepResource{}
	resource.Kind = kind

	result := &Step{}
	result.Resource = resource
	return result
}

func isOrdered(steps []*Step) bool {
	var crdSeen, otherResourceSeen bool
	for _, step := range steps {
		switch step.Resource.Kind {
		case ClusterServiceVersionKind:
			if crdSeen || otherResourceSeen {
				return false
			}
		case crdKind:
			crdSeen = true
			if otherResourceSeen {
				return false
			}
		default:
			otherResourceSeen = true
		}
	}
	return true
}
