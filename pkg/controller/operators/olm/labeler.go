package olm

import (
	"fmt"
	"strings"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/controller/registry/resolver/cache"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// APILabelKeyPrefix is the key prefix for a CSV's APIs label
	APILabelKeyPrefix = "olm.api."
)

type operatorSurface struct {
	ProvidedAPIs cache.APISet
	RequiredAPIs cache.APISet
}

func apiSurfaceOfCSV(csv *v1alpha1.ClusterServiceVersion) (*operatorSurface, error) {
	surface := operatorSurface{
		ProvidedAPIs: cache.EmptyAPISet(),
		RequiredAPIs: cache.EmptyAPISet(),
	}

	for _, crdDef := range csv.Spec.CustomResourceDefinitions.Owned {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("error parsing crd name: %s", crdDef.Name)
		}
		surface.ProvidedAPIs[opregistry.APIKey{Plural: parts[0], Group: parts[1], Version: crdDef.Version, Kind: crdDef.Kind}] = struct{}{}
	}
	for _, api := range csv.Spec.APIServiceDefinitions.Owned {
		surface.ProvidedAPIs[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}

	requiredAPIs := cache.EmptyAPISet()
	for _, crdDef := range csv.Spec.CustomResourceDefinitions.Required {
		parts := strings.SplitN(crdDef.Name, ".", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("error parsing crd name: %s", crdDef.Name)
		}
		requiredAPIs[opregistry.APIKey{Plural: parts[0], Group: parts[1], Version: crdDef.Version, Kind: crdDef.Kind}] = struct{}{}
	}
	for _, api := range csv.Spec.APIServiceDefinitions.Required {
		requiredAPIs[opregistry.APIKey{Group: api.Group, Version: api.Version, Kind: api.Kind, Plural: api.Name}] = struct{}{}
	}

	return &surface, nil
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
	for key := range surface.ProvidedAPIs.StripPlural() {
		hash, err := cache.APIKeyToGVKHash(key)
		if err != nil {
			return nil, err
		}
		labelSet[APILabelKeyPrefix+hash] = "provided"
	}
	for key := range surface.RequiredAPIs.StripPlural() {
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
		hash, err := cache.APIKeyToGVKHash(opregistry.APIKey{
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
		hash, err := cache.APIKeyToGVKHash(opregistry.APIKey{
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
