package resolver

import (
	"testing"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/labels"
)

func TestLabelSetsFor(t *testing.T) {
	tests := []struct {
		name     string
		obj      interface{}
		expected []labels.Set
	}{
		{
			name:     "Nil/Nil",
			obj:      nil,
			expected: nil,
		},
		{
			name:     "NotOperatorSurfaceOrCRD/Nil",
			obj:      struct{ data string }{"some-data"},
			expected: nil,
		},
		{
			name: "CRD/ProvidedAndRequired",
			obj: crd(opregistry.APIKey{
				Group:   "ghouls",
				Version: "v1alpha1",
				Kind:    "Ghost",
				Plural:  "Ghosts",
			}),
			expected: []labels.Set{
				{
					APILabelKeyPrefix + "6435ab0d7c6bda64": "provided",
				},
				{
					APILabelKeyPrefix + "6435ab0d7c6bda64": "required",
				},
			},
		},
		{
			name: "OperatorSurface/Provided",
			obj: &Operator{
				providedAPIs: map[opregistry.APIKey]struct{}{
					opregistry.APIKey{Group: "ghouls", Version: "v1alpha1", Kind: "Ghost", Plural: "Ghosts"}: {},
				},
			},
			expected: []labels.Set{
				{
					APILabelKeyPrefix + "6435ab0d7c6bda64": "provided",
				},
			},
		},
		{
			name: "OperatorSurface/ProvidedAndRequired",
			obj: &Operator{
				providedAPIs: map[opregistry.APIKey]struct{}{
					opregistry.APIKey{Group: "ghouls", Version: "v1alpha1", Kind: "Ghost", Plural: "Ghosts"}: {},
				},
				requiredAPIs: map[opregistry.APIKey]struct{}{
					opregistry.APIKey{Group: "ghouls", Version: "v1alpha1", Kind: "Goblin", Plural: "Goblins"}: {},
				},
			},
			expected: []labels.Set{
				{
					APILabelKeyPrefix + "6435ab0d7c6bda64": "provided",
					APILabelKeyPrefix + "557c9f42470aa352": "required",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labelSets, err := LabelSetsFor(tt.obj)
			require.NoError(t, err)
			require.ElementsMatch(t, tt.expected, labelSets)
		})
	}
}
