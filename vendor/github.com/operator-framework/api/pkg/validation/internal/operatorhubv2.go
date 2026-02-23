package internal

import (
	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

var OperatorHubV2Validator interfaces.Validator = interfaces.ValidatorFunc(validateOperatorHubV2)

func validateOperatorHubV2(objs ...interface{}) (results []errors.ManifestResult) {
	// Obtain the k8s version if informed via the objects an optional
	k8sVersion := ""
	for _, obj := range objs {
		switch obj.(type) {
		case map[string]string:
			k8sVersion = obj.(map[string]string)[k8sVersionKey]
			if len(k8sVersion) > 0 {
				break
			}
		}
	}

	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateBundleOperatorHubV2(v, k8sVersion))
		}
	}

	return results
}

func validateBundleOperatorHubV2(bundle *manifests.Bundle, k8sVersion string) errors.ManifestResult {
	result := errors.ManifestResult{Name: bundle.Name}

	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("Bundle is nil", nil))
		return result
	}

	if bundle.CSV == nil {
		result.Add(errors.ErrInvalidBundle("Bundle csv is nil", bundle.Name))
		return result
	}

	csvChecksResult := validateHubCSVSpecV2(*bundle.CSV)
	for _, err := range csvChecksResult.errs {
		result.Add(errors.ErrInvalidCSV(err.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range csvChecksResult.warns {
		result.Add(errors.WarnInvalidCSV(warn.Error(), bundle.CSV.GetName()))
	}

	errs, warns := validateDeprecatedAPIS(bundle, k8sVersion)
	for _, err := range errs {
		result.Add(errors.ErrFailedValidation(err.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range warns {
		result.Add(errors.WarnFailedValidation(warn.Error(), bundle.CSV.GetName()))
	}

	return result
}

// validateHubCSVSpec will check the CSV against the criteria to publish an
// operator bundle in the OperatorHub.io
func validateHubCSVSpecV2(csv v1alpha1.ClusterServiceVersion) CSVChecks {
	checks := CSVChecks{csv: csv, errs: []error{}, warns: []error{}}

	checks = checkSpecProviderName(checks)
	checks = checkSpecMaintainers(checks)
	checks = checkSpecLinks(checks)
	checks = checkSpecVersion(checks)
	checks = checkSpecIcon(checks)
	checks = checkSpecMinKubeVersion(checks)

	return checks
}
