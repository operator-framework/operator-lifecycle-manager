package resolver

import (
	"fmt"

	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// APILabelKeyPrefix is the key prefix for a CSV's APIs label
	APILabelKeyPrefix = "olm.api."
)

// LabelSetsFor returns API label sets for the given object.
// Concrete types other than OperatorSurface and CustomResource definition no-op.
func LabelSetsFor(obj interface{}) []labels.Set {
	var labelSets []labels.Set
	switch v := obj.(type) {
	case OperatorSurface:
		labelSets = labelSetsForOperatorSurface(v)
	case *extv1beta1.CustomResourceDefinition:
		labelSets = labelSetsForCRD(v)
	}

	return labelSets
}

func labelSetsForOperatorSurface(surface OperatorSurface) []labels.Set {
	labelSet := labels.Set{}
	for key := range surface.ProvidedAPIs().StripPlural() {
		labelSet[APILabelKeyPrefix+APIKeyToGVKString(key)] = "provided"
	}
	for key := range surface.RequiredAPIs().StripPlural() {
		labelSet[APILabelKeyPrefix+APIKeyToGVKString(key)] = "required"
	}

	return []labels.Set{labelSet}
}

func labelSetsForCRD(crd *extv1beta1.CustomResourceDefinition) []labels.Set {
	labelSets := []labels.Set{}
	if crd == nil {
		return labelSets
	}

	// Add label sets for each version
	for _, version := range crd.Spec.Versions {
		key := fmt.Sprintf("%s%s.%s.%s", APILabelKeyPrefix, crd.Spec.Names.Kind, version.Name, crd.Spec.Group)
		sets := []labels.Set{
			{
				key: "provided",
			},
			{
				key: "required",
			},
		}
		labelSets = append(labelSets, sets...)
	}

	return labelSets
}
