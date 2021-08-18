package olm

import (
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	"github.com/operator-framework/operator-registry/pkg/registry"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// APILabelKeyPrefix is the key prefix for a CSV's APIs label
	APILabelKeyPrefix = "olm.api."
)

type operatorSurface interface {
	GetProvidedAPIs() cache.APISet
	GetRequiredAPIs() cache.APISet
}

// LabelSetsFor returns API label sets for the given object.
// Concrete types other than OperatorSurface and CustomResource definition no-op.
func LabelSetsFor(obj interface{}) ([]labels.Set, error) {
	switch v := obj.(type) {
	case operatorSurface:
		return labelSetsForOperatorSurface(v)
	case *extv1beta1.CustomResourceDefinition:
		return labelSetsForCRDv1beta1(v)
	case *extv1.CustomResourceDefinition:
		return labelSetsForCRDv1(v)
	default:
		return nil, nil
	}
}

func labelSetsForOperatorSurface(surface operatorSurface) ([]labels.Set, error) {
	labelSet := labels.Set{}
	for key := range surface.GetProvidedAPIs().StripPlural() {
		hash, err := cache.APIKeyToGVKHash(key)
		if err != nil {
			return nil, err
		}
		labelSet[APILabelKeyPrefix+hash] = "provided"
	}
	for key := range surface.GetRequiredAPIs().StripPlural() {
		hash, err := cache.APIKeyToGVKHash(key)
		if err != nil {
			return nil, err
		}
		labelSet[APILabelKeyPrefix+hash] = "required"
	}

	return []labels.Set{labelSet}, nil
}

func labelSetsForCRDv1beta1(crd *extv1beta1.CustomResourceDefinition) ([]labels.Set, error) {
	labelSets := []labels.Set{}
	if crd == nil {
		return labelSets, nil
	}

	// Add label sets for each version
	for _, version := range crd.Spec.Versions {
		hash, err := cache.APIKeyToGVKHash(registry.APIKey{
			Group:   crd.Spec.Group,
			Version: version.Name,
			Kind:    crd.Spec.Names.Kind,
		})
		if err != nil {
			return nil, err
		}
		key := APILabelKeyPrefix + hash
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

	return labelSets, nil
}

func labelSetsForCRDv1(crd *extv1.CustomResourceDefinition) ([]labels.Set, error) {
	labelSets := []labels.Set{}
	if crd == nil {
		return labelSets, nil
	}

	// Add label sets for each version
	for _, version := range crd.Spec.Versions {
		hash, err := cache.APIKeyToGVKHash(registry.APIKey{
			Group:   crd.Spec.Group,
			Version: version.Name,
			Kind:    crd.Spec.Names.Kind,
		})
		if err != nil {
			return nil, err
		}
		key := APILabelKeyPrefix + hash
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

	return labelSets, nil
}
