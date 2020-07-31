package internal

import (
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"

	"github.com/blang/semver"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/yaml"
)

var CSVValidator interfaces.Validator = interfaces.ValidatorFunc(validateCSVs)

func validateCSVs(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *v1alpha1.ClusterServiceVersion:
			results = append(results, validateCSV(v))
		}
	}
	return results
}

// Iterates over the given CSV. Returns a ManifestResult type object.
func validateCSV(csv *v1alpha1.ClusterServiceVersion) errors.ManifestResult {
	result := errors.ManifestResult{Name: csv.GetName()}
	// Ensure CSV names are of the correct format.
	if _, _, err := parseCSVNameFormat(csv.GetName()); err != (errors.Error{}) {
		result.Add(errors.ErrInvalidCSV(fmt.Sprintf("metadata.name %s", err), csv.GetName()))
	}
	if replaces := csv.Spec.Replaces; replaces != "" {
		if _, _, err := parseCSVNameFormat(replaces); err != (errors.Error{}) {
			result.Add(errors.ErrInvalidCSV(fmt.Sprintf("spec.replaces %s", err), csv.GetName()))
		}
	}
	// validate example annotations ("alm-examples", "olm.examples").
	result.Add(validateExamplesAnnotations(csv)...)
	// validate installModes
	result.Add(validateInstallModes(csv)...)
	// check missing optional/mandatory fields.
	result.Add(checkFields(*csv)...)
	return result
}

func parseCSVNameFormat(name string) (string, semver.Version, error) {
	if violations := k8svalidation.IsDNS1123Subdomain(name); len(violations) != 0 {
		return "", semver.Version{}, fmt.Errorf("%q is invalid:\n%s", name, violations)
	}
	splitName := strings.SplitN(name, ".", 2)
	if len(splitName) != 2 {
		return "", semver.Version{}, fmt.Errorf("%q must have format: {operator name}.(v)X.Y.Z", name)
	}
	verStr := strings.TrimLeft(splitName[1], "v")
	nameVer, err := semver.Parse(verStr)
	if err != nil {
		return "", semver.Version{}, fmt.Errorf("%q contains an invalid semver %q", name, splitName[1])
	}
	return splitName[0], nameVer, errors.Error{}
}

// checkFields runs checkEmptyFields and returns its errors.
func checkFields(csv v1alpha1.ClusterServiceVersion) (errs []errors.Error) {
	result := errors.ManifestResult{}
	checkEmptyFields(&result, reflect.ValueOf(csv), "")
	return append(result.Errors, result.Warnings...)
}

// validateExamplesAnnotations compares alm/olm example annotations with provided APIs given
// by Spec.CustomResourceDefinitions.Owned and Spec.APIServiceDefinitions.Owned.
func validateExamplesAnnotations(csv *v1alpha1.ClusterServiceVersion) (errs []errors.Error) {
	annotations := csv.ObjectMeta.GetAnnotations()
	// Return right away if no examples annotations are found.
	if len(annotations) == 0 {
		errs = append(errs, errors.WarnInvalidCSV("annotations not found", csv.GetName()))
		return errs
	}
	// Expect either `alm-examples` or `olm.examples` but not both
	// If both are present, `alm-examples` will be used
	var examplesString string
	almExamples, almOK := annotations["alm-examples"]
	olmExamples, olmOK := annotations["olm.examples"]
	if !almOK && !olmOK {
		errs = append(errs, errors.WarnInvalidCSV("example annotations not found", csv.GetName()))
		return errs
	} else if almOK {
		if olmOK {
			errs = append(errs, errors.WarnInvalidCSV("both `alm-examples` and `olm.examples` are present. Checking only `alm-examples`", csv.GetName()))
		}
		examplesString = almExamples
	} else {
		examplesString = olmExamples
	}
	us := []unstructured.Unstructured{}
	dec := yaml.NewYAMLOrJSONDecoder(strings.NewReader(examplesString), 8)
	if err := dec.Decode(&us); err != nil && err != io.EOF {
		errs = append(errs, errors.ErrInvalidParse("error decoding example CustomResource", err))
		return errs
	}
	parsed := map[schema.GroupVersionKind]struct{}{}
	for _, u := range us {
		parsed[u.GetObjectKind().GroupVersionKind()] = struct{}{}
	}

	providedAPISet, aerrs := getProvidedAPIs(csv)
	errs = append(errs, aerrs...)

	errs = append(errs, matchGVKProvidedAPIs(parsed, providedAPISet)...)
	return errs
}

func getProvidedAPIs(csv *v1alpha1.ClusterServiceVersion) (provided map[schema.GroupVersionKind]struct{}, errs []errors.Error) {
	provided = map[schema.GroupVersionKind]struct{}{}
	for _, owned := range csv.Spec.CustomResourceDefinitions.Owned {
		parts := strings.SplitN(owned.Name, ".", 2)
		if len(parts) < 2 {
			errs = append(errs, errors.ErrInvalidParse(fmt.Sprintf("couldn't parse plural.group from crd name: %s", owned.Name), nil))
			continue
		}
		provided[newGVK(parts[1], owned.Version, owned.Kind)] = struct{}{}
	}

	for _, api := range csv.Spec.APIServiceDefinitions.Owned {
		provided[newGVK(api.Group, api.Version, api.Kind)] = struct{}{}
	}

	return provided, errs
}

func newGVK(g, v, k string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: g, Version: v, Kind: k}
}

func matchGVKProvidedAPIs(exampleSet map[schema.GroupVersionKind]struct{}, providedAPISet map[schema.GroupVersionKind]struct{}) (errs []errors.Error) {
	for example := range exampleSet {
		if _, ok := providedAPISet[example]; !ok {
			errs = append(errs, errors.ErrInvalidOperation("example must have a provided API", example))
		}
	}
	for api := range providedAPISet {
		if _, ok := exampleSet[api]; !ok {
			errs = append(errs, errors.WarnInvalidOperation("provided API should have an example annotation", api))
		}
	}
	return errs
}

func validateInstallModes(csv *v1alpha1.ClusterServiceVersion) (errs []errors.Error) {
	if len(csv.Spec.InstallModes) == 0 {
		errs = append(errs, errors.ErrInvalidCSV("install modes not found", csv.GetName()))
		return errs
	}

	installModeSet := v1alpha1.InstallModeSet{}
	anySupported := false
	for _, installMode := range csv.Spec.InstallModes {
		if _, ok := installModeSet[installMode.Type]; ok {
			errs = append(errs, errors.ErrInvalidCSV("duplicate install modes present", csv.GetName()))
		} else if installMode.Supported {
			anySupported = true
		}
	}

	// validate installModes when conversionCRDs field is present in csv.Spec.Webhookdefinitions
	// check if WebhookDefinitions is present
	if len(csv.Spec.WebhookDefinitions) != 0 {
		for _, WebhookDefinition := range csv.Spec.WebhookDefinitions {
			// check if ConversionCRDs is present
			if len(WebhookDefinition.ConversionCRDs) != 0 {
				supportsOnlyAllNamespaces := true
				// check if AllNamespaces is supported and other install modes are not supported
				for _, installMode := range csv.Spec.InstallModes {
					if installMode.Type == "AllNamespaces" && !installMode.Supported {
						supportsOnlyAllNamespaces = false
					}
					if installMode.Type != "AllNamespaces" && installMode.Supported {
						supportsOnlyAllNamespaces = false
					}
				}
				if supportsOnlyAllNamespaces == false {
					errs = append(errs, errors.ErrInvalidCSV("only AllNamespaces InstallModeType is supported when conversionCRDs is present", csv.GetName()))
				}
			}
		}
	}

	// all installModes should not be `false`
	if !anySupported {
		errs = append(errs, errors.ErrInvalidCSV("none of InstallModeTypes are supported", csv.GetName()))
	}
	return errs
}
