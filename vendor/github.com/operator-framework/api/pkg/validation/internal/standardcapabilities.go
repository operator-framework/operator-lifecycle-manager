package internal

import (
	"fmt"

	"github.com/operator-framework/api/pkg/manifests"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

var StandardCapabilitiesValidator interfaces.Validator = interfaces.ValidatorFunc(validateCapabilities)

var validCapabilities = map[string]struct{}{
	"Basic Install":     {},
	"Seamless Upgrades": {},
	"Full Lifecycle":    {},
	"Deep Insights":     {},
	"Auto Pilot":        {},
}

func validateCapabilities(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateCapabilitiesBundle(v))
		}
	}

	return results
}

func validateCapabilitiesBundle(bundle *manifests.Bundle) errors.ManifestResult {
	result := errors.ManifestResult{Name: bundle.Name}
	csvCategoryCheck := CSVChecks{csv: *bundle.CSV, errs: []error{}, warns: []error{}}

	csvChecksResult := checkCapabilities(csvCategoryCheck)
	for _, err := range csvChecksResult.errs {
		result.Add(errors.ErrInvalidCSV(err.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range csvChecksResult.warns {
		result.Add(errors.WarnInvalidCSV(warn.Error(), bundle.CSV.GetName()))
	}

	return result
}

// checkAnnotations will validate the values informed via annotations such as; capabilities and categories
func checkCapabilities(checks CSVChecks) CSVChecks {
	if checks.csv.GetAnnotations() == nil {
		checks.csv.SetAnnotations(make(map[string]string))
	}

	if capability, ok := checks.csv.ObjectMeta.Annotations["capabilities"]; ok {
		if _, ok := validCapabilities[capability]; !ok {
			checks.errs = append(checks.errs, fmt.Errorf("csv.Metadata.Annotations.Capabilities %q is not a valid capabilities level", capability))
		}
	}
	return checks
}
