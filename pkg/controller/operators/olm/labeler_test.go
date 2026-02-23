package olm

import (
	"testing"

	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	"github.com/stretchr/testify/require"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
			obj: &v1beta1.CustomResourceDefinition{
				TypeMeta: metav1.TypeMeta{
					Kind:       "CustomResourceDefinition",
					APIVersion: v1beta1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "Ghosts.ghouls",
				},
				Spec: v1beta1.CustomResourceDefinitionSpec{
					Group: "ghouls",
					Versions: []v1beta1.CustomResourceDefinitionVersion{
						{
							Name:    "v1alpha1",
							Storage: true,
							Served:  true,
						},
					},
					Names: v1beta1.CustomResourceDefinitionNames{
						Kind:   "Ghost",
						Plural: "Ghosts",
					},
				},
			},
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
			obj: operatorSurface{
				ProvidedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "ghouls", Version: "v1alpha1", Kind: "Ghost", Plural: "Ghosts"}: {},
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
			obj: operatorSurface{
				ProvidedAPIs: map[opregistry.APIKey]struct{}{
					{Group: "ghouls", Version: "v1alpha1", Kind: "Ghost", Plural: "Ghosts"}: {},
				},
				RequiredAPIs: map[opregistry.APIKey]struct{}{
					{Group: "ghouls", Version: "v1alpha1", Kind: "Goblin", Plural: "Goblins"}: {},
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
