package internal

import (
	"fmt"
	"strings"

	"github.com/operator-framework/api/pkg/manifests"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var BundleValidator interfaces.Validator = interfaces.ValidatorFunc(validateBundles)

func validateBundles(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateBundle(v))
		}
	}
	return results
}

func validateBundle(bundle *manifests.Bundle) (result errors.ManifestResult) {
	result = validateOwnedCRDs(bundle, bundle.CSV)
	result.Name = bundle.CSV.Spec.Version.String()
	saErrors := validateServiceAccounts(bundle)
	if saErrors != nil {
		result.Add(saErrors...)
	}
	return result
}

func validateServiceAccounts(bundle *manifests.Bundle) []errors.Error {
	// get service account names defined in the csv
	saNamesFromCSV := make(map[string]struct{}, 0)
	for _, deployment := range bundle.CSV.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
		saName := deployment.Spec.Template.Spec.ServiceAccountName
		saNamesFromCSV[saName] = struct{}{}
	}

	// find any hardcoded service account objects are in the bundle, then check if they match any sa definition in the csv
	var errs []errors.Error
	for _, obj := range bundle.Objects {
		if obj.GroupVersionKind() != v1.SchemeGroupVersion.WithKind("ServiceAccount") {
			continue
		}
		sa := v1.ServiceAccount{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &sa); err == nil {
			if _, ok := saNamesFromCSV[sa.Name]; ok {
				errs = append(errs, errors.ErrInvalidBundle(fmt.Sprintf("invalid service account found in bundle. "+
					"This service account %s in your bundle is not valid, because a service account with the same name "+
					"was already specified in your CSV. If this was unintentional, please remove the service account "+
					"manifest from your bundle. If it was intentional to specify a separate service account, "+
					"please rename the SA in either the bundle manifest or the CSV.", sa.Name), sa.Name))
			}
		}
	}

	return errs
}

func validateOwnedCRDs(bundle *manifests.Bundle, csv *operatorsv1alpha1.ClusterServiceVersion) (result errors.ManifestResult) {
	ownedKeys := getOwnedCustomResourceDefintionKeys(csv)

	// Check for duplicate keys in the bundle, which may occur if a v1 and v1beta1 CRD of the same GVK appear.
	keySet := make(map[schema.GroupVersionKind]struct{})
	for _, key := range getBundleCRDKeys(bundle) {
		if _, hasKey := keySet[key]; hasKey {
			result.Add(errors.ErrInvalidBundle(fmt.Sprintf("duplicate CRD %q in bundle %q", key, bundle.Name), key))
		}
		// Always add key to keySet so the below validations run correctly.
		keySet[key] = struct{}{}
	}

	// All owned keys must match a CRD in bundle.
	ownedGVSet := make(map[schema.GroupKind]struct{})
	for _, ownedKey := range ownedKeys {
		if _, ok := keySet[ownedKey]; !ok {
			result.Add(errors.ErrInvalidBundle(fmt.Sprintf("owned CRD %q not found in bundle %q", ownedKey, bundle.Name), ownedKey))
		} else {
			delete(keySet, ownedKey)
			gvKey := schema.GroupKind{Group: ownedKey.Group, Kind: ownedKey.Kind}
			ownedGVSet[gvKey] = struct{}{}
		}
	}

	// Filter out unused versions of the same CRD
	for key := range keySet {
		gvKey := schema.GroupKind{Group: key.Group, Kind: key.Kind}
		if _, ok := ownedGVSet[gvKey]; ok {
			delete(keySet, key)
		}
	}

	// All CRDs present in a CSV must be present in the bundle.
	for key := range keySet {
		result.Add(errors.WarnInvalidBundle(fmt.Sprintf("CRD %q is present in bundle %q but not defined in CSV", key, bundle.Name), key))
	}

	return result
}

// getBundleCRDKeys returns a list of definition keys for all owned CRDs in csv.
func getOwnedCustomResourceDefintionKeys(csv *operatorsv1alpha1.ClusterServiceVersion) (keys []schema.GroupVersionKind) {
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		group := owned.Name
		if split := strings.SplitN(group, ".", 2); len(split) == 2 {
			group = split[1]
		}
		keys = append(keys, schema.GroupVersionKind{Group: group, Version: owned.Version, Kind: owned.Kind})
	}
	return keys
}

// getBundleCRDKeys returns a set of definition keys for all CRDs in bundle.
func getBundleCRDKeys(bundle *manifests.Bundle) (keys []schema.GroupVersionKind) {
	// Collect all v1 and v1beta1 CRD keys, skipping group which CSVs do not support.
	for _, crd := range bundle.V1CRDs {
		for _, version := range crd.Spec.Versions {
			keys = append(keys, schema.GroupVersionKind{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind})
		}
	}
	for _, crd := range bundle.V1beta1CRDs {
		if len(crd.Spec.Versions) == 0 {
			keys = append(keys, schema.GroupVersionKind{Group: crd.Spec.Group, Version: crd.Spec.Version, Kind: crd.Spec.Names.Kind})
		} else {
			for _, version := range crd.Spec.Versions {
				keys = append(keys, schema.GroupVersionKind{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind})
			}
		}
	}
	return keys
}
