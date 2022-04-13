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

// max_bundle_size is the maximum size of a bundle in bytes.
// This ensures the bundle can be staged in a single ConfigMap by OLM during installation.
// The value is derived from the standard upper bound for k8s resources (~1MB).
// We will use this value to check the bundle compressed is < ~1MB
const max_bundle_size = int64(1 << (10 * 2))

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
	sizeErrors := validateBundleSize(bundle)
	if sizeErrors != nil {
		result.Add(sizeErrors...)
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
		result.Add(errors.ErrInvalidBundle(fmt.Sprintf("CRD %q is present in bundle %q but not defined in CSV", key, bundle.Name), key))
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

// validateBundleSize will check the bundle size according to its limits
// note that this check will raise an error if the size is bigger than the max allowed
// and warnings when:
// - we are unable to check the bundle size because we are running a check without load the bundle
// - we could identify that the bundle size is close to the limit (bigger than 85%)
func validateBundleSize(bundle *manifests.Bundle) []errors.Error {
	warnPercent := 0.85
	warnSize := float64(max_bundle_size) * warnPercent
	var errs []errors.Error

	if bundle.CompressedSize == 0 {
		errs = append(errs, errors.WarnFailedValidation("unable to check the bundle compressed size", bundle.Name))
		return errs
	}

	if bundle.Size == 0 {
		errs = append(errs, errors.WarnFailedValidation("unable to check the bundle size", bundle.Name))
		return errs
	}

	// From OPM (https://github.com/operator-framework/operator-registry) 1.17.5
	// and OLM (https://github.com/operator-framework/operator-lifecycle-manager) : v0.19.0
	// the total size checked is compressed
	if bundle.CompressedSize > max_bundle_size {
		errs = append(errs, errors.ErrInvalidBundle(
			fmt.Sprintf("maximum bundle compressed size with gzip size exceeded: size=~%s , max=%s. Bundle uncompressed size is %s",
				formatBytesInUnit(bundle.CompressedSize),
				formatBytesInUnit(max_bundle_size),
				formatBytesInUnit(bundle.Size)),
			bundle.Name))
	} else if float64(bundle.CompressedSize) > warnSize {
		errs = append(errs, errors.WarnInvalidBundle(
			fmt.Sprintf("nearing maximum bundle compressed size with gzip: size=~%s , max=%s. Bundle uncompressed size is %s",
				formatBytesInUnit(bundle.CompressedSize),
				formatBytesInUnit(max_bundle_size),
				formatBytesInUnit(bundle.Size)),
			bundle.Name))
	}

	return errs
}

func formatBytesInUnit(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "kMGTPE"[exp])
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
