package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/mail"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// OperatorHubValidator validates the bundle manifests against the required criteria to publish
// the projects on OperatorHub.io.
//
// This validator will ensure that:
//
// - The annotations capabilities into the CSV has a valid option, which are:
//
// * Basic Install
//
// * Seamless Upgrades
//
// * Full Lifecycle
//
// * Deep Insights
//
// * Auto Pilot
//
// - The annotations categories into the CSV has a valid option, which are:
//
// * AI/Machine Learning
//
// * Application Runtime
//
// * Big Data
//
// * Cloud Provider
//
// * Developer Tools
//
// * Database
//
// * Integration & Delivery
//
// * Logging & Tracing
//
// * Modernization & Migration
//
// * Monitoring
//
// * Networking
//
// * OpenShift Optional
//
// * Security
//
// * Storage
//
// * Streaming & Messaging
//
// NOTE: The OperatorHub validator can verify against custom bundle categories by setting the OPERATOR_BUNDLE_CATEGORIES
// environment variable. Setting the OPERATOR_BUNDLE_CATEGORIES environment variable to the path to a json file
// containing a list of categories will enable those categories to be used when comparing CSV categories for
// OperatorHub validation. The json file should be in the following format:
//
//		```json
//		{
//			"categories":[
//	     "Cloud Pak",
//	     "Registry",
//	     "MyCoolThing",
//	 	 ]
//		}
//		```
//
// - The `csv.Spec.Provider.Name` was provided
//
// - The `csv.Spec.Maintainers` elements contains both name and email
//
// - The `csv.Spec.Links` elements contains both name and url
//
// - The `csv.Spec.Links.Url` is a valid value
//
// - The `csv.Spec.Version` is provided
//
// - The `csv.Spec.Icon` was provided and has not more than one element
//
// - The `csv.Spec.Icon` elements should contain both data and `mediatype`
//
// - The `csv.Spec.Icon` elements should contain both data and `mediatype`
//
// - The `csv.Spec.Icon` has a valid `mediatype`, which are
//
// * image/gif
//
// * image/jpeg
//
// * image/png
//
// * image/svg+xml
//
// - If informed ONLY, check if the value csv.Spec.MinKubeVersion is parsable according to semver (https://semver.org/)
// Also, this validator will raise warnings when:
//
// - The channel names seems are not following the convention https://olm.operatorframework.io/docs/best-practices/channel-naming/
//
// - The usage of the removed APIs on Kubernetes 1.22 is found. More info: https://kubernetes.io/docs/reference/using-api/deprecation-guide/#v1-22
//
// Note that this validator allows to receive a List of optional values as key=values. Currently, only the
// `k8s-version` key is allowed. If informed, it will perform the checks against this specific Kubernetes version where the
// operator bundle is intend to be used and will raise errors instead of warnings.
// Currently, this check is capable of verifying the removed APIs only for Kubernetes 1.22 version.
//
// Deprecated: Use OperatorHubV2Validator, StandardCapabilitiesValidator and StandardCategoriesValidator for equivalent validation.
var OperatorHubValidator interfaces.Validator = interfaces.ValidatorFunc(validateOperatorHubDeprecated)

var validMediatypes = map[string]struct{}{
	"image/gif":     {},
	"image/jpeg":    {},
	"image/png":     {},
	"image/svg+xml": {},
}

const minKubeVersionWarnMessage = "csv.Spec.minKubeVersion is not informed. It is recommended you provide this information. " +
	"Otherwise, it would mean that your operator project can be distributed and installed in any cluster version " +
	"available, which is not necessarily the case for all projects."

// Warning: this validator is deprecated in favor of validateOperatorHub()
func validateOperatorHubDeprecated(objs ...interface{}) (results []errors.ManifestResult) {

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
			results = append(results, validateBundleOperatorHub(v, k8sVersion))
		}
	}

	// Add a deprecation warning to the list so that users are aware this validator is deprecated
	deprecationResultWarning := errors.ManifestResult{}
	deprecationResultWarning.Add(errors.WarnDeprecatedValidator(`The "operatorhub" validator is deprecated; for equivalent validation use "operatorhub/v2", "standardcapabilities" and "standardcategories" validators`))
	results = append(results, deprecationResultWarning)

	return results
}

func validateBundleOperatorHub(bundle *manifests.Bundle, k8sVersion string) errors.ManifestResult {
	result := errors.ManifestResult{Name: bundle.Name}

	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("Bundle is nil", nil))
		return result
	}

	if bundle.CSV == nil {
		result.Add(errors.ErrInvalidBundle("Bundle csv is nil", bundle.Name))
		return result
	}

	csvChecksResult := validateHubCSVSpec(*bundle.CSV)
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
func validateHubCSVSpec(csv v1alpha1.ClusterServiceVersion) CSVChecks {
	checks := CSVChecks{csv: csv, errs: []error{}, warns: []error{}}

	checks = checkSpecProviderName(checks)
	checks = checkSpecMaintainers(checks)
	checks = checkSpecLinks(checks)
	checks = checkCapabilities(checks)
	checks = checkCategories(checks)
	checks = checkSpecVersion(checks)
	checks = checkSpecIcon(checks)
	checks = checkSpecMinKubeVersion(checks)

	return checks
}

type CSVChecks struct {
	csv   v1alpha1.ClusterServiceVersion
	errs  []error
	warns []error
}

// checkSpecMinKubeVersion will validate the spec minKubeVersion informed via CSV.spec.minKubeVersion
func checkSpecMinKubeVersion(checks CSVChecks) CSVChecks {
	if len(strings.TrimSpace(checks.csv.Spec.MinKubeVersion)) == 0 {
		checks.warns = append(checks.warns, fmt.Errorf(minKubeVersionWarnMessage))
	} else {
		if _, err := semver.Parse(checks.csv.Spec.MinKubeVersion); err != nil {
			checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.MinKubeVersion has an invalid value: %s", checks.csv.Spec.MinKubeVersion))
		}
	}
	return checks
}

// checkSpecVersion will validate the spec Version informed via CSV.spec.Version
func checkSpecVersion(checks CSVChecks) CSVChecks {
	// spec.Version needs to be set
	if checks.csv.Spec.Version.Equals(semver.Version{}) {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Version must be set"))
	}
	return checks
}

// checkSpecIcon will validate if the CSV.spec.Icon was informed and is correct
func checkSpecIcon(checks CSVChecks) CSVChecks {
	if checks.csv.Spec.Icon != nil {
		// only one icon is allowed
		if len(checks.csv.Spec.Icon) != 1 {
			checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Icon should only have one element"))
		}

		icon := checks.csv.Spec.Icon[0]
		if icon.MediaType == "" || icon.Data == "" {
			checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Icon elements should contain both data and mediatype"))
		}

		if icon.MediaType != "" {
			if _, ok := validMediatypes[icon.MediaType]; !ok {
				checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Icon %s does not have a valid mediatype", icon.MediaType))
			}
		}
	} else {
		checks.warns = append(checks.warns, fmt.Errorf("csv.Spec.Icon not specified"))
	}
	return checks
}

// checkSpecLinks will validate the value informed via csv.Spec.Links
func checkSpecLinks(checks CSVChecks) CSVChecks {
	for _, link := range checks.csv.Spec.Links {
		if link.Name == "" || link.URL == "" {
			checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Links elements should contain both name and url"))
		}
		if link.URL != "" {
			_, err := url.ParseRequestURI(link.URL)
			if err != nil {
				checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Links url %s is invalid: %v", link.URL, err))
			}
		}
	}
	return checks
}

// checkSpecMaintainers will validate the values informed via csv.Spec.Maintainers
func checkSpecMaintainers(checks CSVChecks) CSVChecks {
	for _, maintainer := range checks.csv.Spec.Maintainers {
		if maintainer.Name == "" || maintainer.Email == "" {
			checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Maintainers elements should contain both name and email"))
		}
		if maintainer.Email != "" {
			_, err := mail.ParseAddress(maintainer.Email)
			if err != nil {
				checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Maintainers email %s is invalid: %v", maintainer.Email, err))
			}
		}
	}
	return checks
}

// checkSpecProviderName will validate the values informed via csv.Spec.Provider.Name
func checkSpecProviderName(checks CSVChecks) CSVChecks {
	if strings.TrimSpace(checks.csv.Spec.Provider.Name) == "" {
		checks.errs = append(checks.errs, fmt.Errorf("csv.Spec.Provider.Name not specified"))
	}
	return checks
}

type categories struct {
	Contents []string `json:"categories"`
}

// extractCategories reads a custom categories file and returns the contents in a map[string]struct{}
func extractCategories(path string) (map[string]struct{}, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("finding category file: %w", err)
	}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading category file: %w", err)
	}

	cat := categories{}
	err = json.Unmarshal(data, &cat)
	if err != nil {
		return nil, fmt.Errorf("unmarshaling category file: %w", err)
	}

	customCategories := make(map[string]struct{})
	for _, c := range cat.Contents {
		customCategories[c] = struct{}{}
	}
	return customCategories, nil
}
