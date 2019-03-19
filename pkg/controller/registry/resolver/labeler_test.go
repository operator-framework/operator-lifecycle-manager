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
					APILabelKeyPrefix + "Ghost.v1alpha1.ghouls": "provided",
				},
				{
					APILabelKeyPrefix + "Ghost.v1alpha1.ghouls": "required",
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
					APILabelKeyPrefix + "Ghost.v1alpha1.ghouls": "provided",
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
					APILabelKeyPrefix + "Ghost.v1alpha1.ghouls":  "provided",
					APILabelKeyPrefix + "Goblin.v1alpha1.ghouls": "required",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ElementsMatch(t, tt.expected, LabelSetsFor(tt.obj))
		})
	}
}
