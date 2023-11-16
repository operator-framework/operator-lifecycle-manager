package internal

import (
	"fmt"
	"os"
	"strings"

	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

var StandardCategoriesValidator interfaces.Validator = interfaces.ValidatorFunc(validateCategories)

var validCategories = map[string]struct{}{
	"AI/Machine Learning":       {},
	"Application Runtime":       {},
	"Big Data":                  {},
	"Cloud Provider":            {},
	"Developer Tools":           {},
	"Database":                  {},
	"Integration & Delivery":    {},
	"Logging & Tracing":         {},
	"Monitoring":                {},
	"Modernization & Migration": {},
	"Networking":                {},
	"OpenShift Optional":        {},
	"Security":                  {},
	"Storage":                   {},
	"Streaming & Messaging":     {},
	"Observability":             {},
}

func validateCategories(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateCategoriesBundle(v))
		}
	}

	return results
}

func validateCategoriesBundle(bundle *manifests.Bundle) errors.ManifestResult {
	result := errors.ManifestResult{Name: bundle.Name}
	csvCategoryCheck := CSVChecks{csv: *bundle.CSV, errs: []error{}, warns: []error{}}

	csvChecksResult := checkCategories(csvCategoryCheck)
	for _, err := range csvChecksResult.errs {
		result.Add(errors.ErrInvalidCSV(err.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range csvChecksResult.warns {
		result.Add(errors.WarnInvalidCSV(warn.Error(), bundle.CSV.GetName()))
	}

	return result
}

func checkCategories(checks CSVChecks) CSVChecks {
	if checks.csv.GetAnnotations() == nil {
		checks.csv.SetAnnotations(make(map[string]string))
	}

	if categories, ok := checks.csv.ObjectMeta.Annotations["categories"]; ok {
		categorySlice := strings.Split(categories, ",")

		// use custom categories for validation if provided
		customCategoriesPath := os.Getenv("OPERATOR_BUNDLE_CATEGORIES")
		if customCategoriesPath != "" {
			customCategories, err := extractCategories(customCategoriesPath)
			if err != nil {
				checks.errs = append(checks.errs, fmt.Errorf("could not extract custom categories from categories %#v: %s", customCategories, err))
			} else {
				for _, category := range categorySlice {
					if _, ok := customCategories[strings.TrimSpace(category)]; !ok {
						checks.errs = append(checks.errs, fmt.Errorf("csv.Metadata.Annotations[\"categories\"] value %q is not in the set of custom categories", category))
					}
				}
			}
		} else {
			// use default categories
			for _, category := range categorySlice {
				if _, ok := validCategories[strings.TrimSpace(category)]; !ok {
					checks.errs = append(checks.errs, fmt.Errorf("csv.Metadata.Annotations[\"categories\"] value %q is not in the set of standard categories", category))
				}
			}
		}
	}
	return checks
}
