package validation

import (
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

var RegistryBundleValidator interfaces.Validator = interfaces.ValidatorFunc(validateBundles)

func validateBundles(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *registry.Bundle:
			results = append(results, validateBundle(v))
		}
	}
	return results
}

func validateBundle(bundle *registry.Bundle) (result errors.ManifestResult) {
	csv, err := bundle.ClusterServiceVersion()
	if err != nil {
		result.Add(errors.ErrInvalidParse("error getting bundle CSV", err))
		return result
	}

	result = validateOwnedCRDs(bundle, csv)

	if result.Name, err = csv.GetVersion(); err != nil {
		result.Add(errors.ErrInvalidParse("error getting bundle CSV version", err))
		return result
	}
	return result
}

func validateOwnedCRDs(bundle *registry.Bundle, csv *registry.ClusterServiceVersion) (result errors.ManifestResult) {
	ownedKeys, _, err := csv.GetCustomResourceDefintions()
	if err != nil {
		result.Add(errors.ErrInvalidParse("error getting CSV CRDs", err))
		return result
	}

	keySet, rerr := getBundleCRDKeys(bundle)
	if rerr != (errors.Error{}) {
		result.Add(rerr)
		return result
	}

	// Validate all owned keys, and remove them from the set if seen.
	for _, ownedKey := range ownedKeys {
		if _, ok := keySet[*ownedKey]; !ok {
			result.Add(errors.ErrInvalidBundle(fmt.Sprintf("owned CRD %s not found in bundle %q", keyToString(*ownedKey), bundle.Name), *ownedKey))
		} else {
			delete(keySet, *ownedKey)
		}
	}
	// CRDs not defined in the CSV present in the bundle
	for key := range keySet {
		result.Add(errors.WarnInvalidBundle(fmt.Sprintf("CRD %s is present in bundle %q but not defined in CSV", keyToString(key), bundle.Name), key))
	}
	return result
}

func getBundleCRDKeys(bundle *registry.Bundle) (map[registry.DefinitionKey]struct{}, errors.Error) {
	crds, err := bundle.CustomResourceDefinitions()
	if err != nil {
		return nil, errors.ErrInvalidParse("error getting bundle CRDs", err)
	}
	keySet := map[registry.DefinitionKey]struct{}{}
	for _, c := range crds {
		switch crd := c.(type) {
		case *apiextensionsv1.CustomResourceDefinition:
			for _, version := range crd.Spec.Versions {
				// Skip group, which CSVs do not support.
				key := registry.DefinitionKey{
					Name:    crd.GetName(),
					Version: version.Name,
					Kind:    crd.Spec.Names.Kind,
				}
				keySet[key] = struct{}{}
			}
		case *apiextensionsv1beta1.CustomResourceDefinition:
			if crd.Spec.Version != "" {
				key := registry.DefinitionKey{
					Name:    crd.GetName(),
					Version: crd.Spec.Version,
					Kind:    crd.Spec.Names.Kind,
				}
				keySet[key] = struct{}{}
			} else {
				for _, version := range crd.Spec.Versions {
					// Skip group, which CSVs do not support.
					key := registry.DefinitionKey{
						Name:    crd.GetName(),
						Version: version.Name,
						Kind:    crd.Spec.Names.Kind,
					}
					keySet[key] = struct{}{}
				}
			}
		}
	}
	return keySet, errors.Error{}
}

func keyToString(key registry.DefinitionKey) string {
	if key.Name == "" {
		return fmt.Sprintf("%s/%s %s", key.Group, key.Version, key.Kind)
	}
	return fmt.Sprintf("%s/%s", key.Name, key.Version)
}
