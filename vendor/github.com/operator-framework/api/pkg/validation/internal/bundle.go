package internal

import (
	"encoding/json"
	"fmt"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"

	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/registry"
)

var BundleValidator interfaces.Validator = interfaces.ValidatorFunc(validateBundles)

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
	bcsv, err := bundle.ClusterServiceVersion()
	if err != nil {
		result.Add(errors.ErrInvalidParse("error getting bundle CSV", err))
		return result
	}
	csv, rerr := bundleCSVToCSV(bcsv)
	if rerr != (errors.Error{}) {
		result.Add(rerr)
		return result
	}
	result = validateOwnedCRDs(bundle, csv)
	result.Name = csv.Spec.Version.String()
	return result
}

func bundleCSVToCSV(bcsv *registry.ClusterServiceVersion) (*operatorsv1alpha1.ClusterServiceVersion, errors.Error) {
	spec := operatorsv1alpha1.ClusterServiceVersionSpec{}
	if err := json.Unmarshal(bcsv.Spec, &spec); err != nil {
		return nil, errors.ErrInvalidParse(fmt.Sprintf("converting bundle CSV %q", bcsv.GetName()), err)
	}
	return &operatorsv1alpha1.ClusterServiceVersion{
		TypeMeta:   bcsv.TypeMeta,
		ObjectMeta: bcsv.ObjectMeta,
		Spec:       spec,
	}, errors.Error{}
}

func validateOwnedCRDs(bundle *registry.Bundle, csv *operatorsv1alpha1.ClusterServiceVersion) (result errors.ManifestResult) {
	ownedCrdNames := getOwnedCustomResourceDefintionNames(csv)
	crdNames, err := getBundleCRDNames(bundle)
	if err != (errors.Error{}) {
		result.Add(err)
		return result
	}

	// validating names
	for _, crdName := range ownedCrdNames {
		if _, ok := crdNames[crdName]; !ok {
			result.Add(errors.ErrInvalidBundle(fmt.Sprintf("owned CRD %q not found in bundle %q", crdName, bundle.Name), crdName))
		} else {
			delete(crdNames, crdName)
		}
	}
	// CRDs not defined in the CSV present in the bundle
	for crdName := range crdNames {
		result.Add(errors.WarnInvalidBundle(fmt.Sprintf("owned CRD %q is present in bundle %q but not defined in CSV", crdName, bundle.Name), crdName))
	}
	return result
}

func getOwnedCustomResourceDefintionNames(csv *operatorsv1alpha1.ClusterServiceVersion) (names []string) {
	for _, ownedCrd := range csv.Spec.CustomResourceDefinitions.Owned {
		names = append(names, ownedCrd.Name)
	}
	return names
}

func getBundleCRDNames(bundle *registry.Bundle) (map[string]struct{}, errors.Error) {
	crds, err := bundle.CustomResourceDefinitions()
	if err != nil {
		return nil, errors.ErrInvalidParse("error getting bundle CRDs", err)
	}
	crdNames := map[string]struct{}{}
	for _, crd := range crds {
		crdNames[crd.GetName()] = struct{}{}
	}
	return crdNames, errors.Error{}
}
