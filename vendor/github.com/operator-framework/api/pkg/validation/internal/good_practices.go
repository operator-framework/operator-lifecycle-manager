package internal

import (
	goerrors "errors"
	"fmt"
	"strings"

	"github.com/operator-framework/api/pkg/manifests"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// GoodPracticesValidator validates the bundle against criteria and suggestions defined as
// good practices for bundles under the operator-framework solutions. (You might give a
// look at https://sdk.operatorframework.io/docs/best-practices/)
//
// This validator will raise an WARNING when:
//
// - The resources request for CPU and/or Memory are not defined for any of the containers found in the CSV
//
// - The channel names seems are not following the convention https://olm.operatorframework.io/docs/best-practices/channel-naming/
//
var GoodPracticesValidator interfaces.Validator = interfaces.ValidatorFunc(goodPracticesValidator)

func goodPracticesValidator(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *manifests.Bundle:
			results = append(results, validateGoodPracticesFrom(v))
		}
	}
	return results
}

func validateGoodPracticesFrom(bundle *manifests.Bundle) errors.ManifestResult {
	result := errors.ManifestResult{}
	if bundle == nil {
		result.Add(errors.ErrInvalidBundle("Bundle is nil", nil))
		return result
	}

	result.Name = bundle.Name

	if bundle.CSV == nil {
		result.Add(errors.ErrInvalidBundle("Bundle csv is nil", bundle.Name))
		return result
	}

	errs, warns := validateResourceRequests(bundle.CSV)
	for _, err := range errs {
		result.Add(errors.ErrFailedValidation(err.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range warns {
		result.Add(errors.WarnFailedValidation(warn.Error(), bundle.CSV.GetName()))
	}
	for _, warn := range validateCrdDescriptions(bundle.CSV.Spec.CustomResourceDefinitions) {
		result.Add(errors.WarnFailedValidation(warn.Error(), bundle.CSV.GetName()))
	}

	channels := append(bundle.Channels, bundle.DefaultChannel)
	if warn := validateHubChannels(channels); warn != nil {
		result.Add(errors.WarnFailedValidation(warn.Error(), bundle.CSV.GetName()))
	}
	return result
}

// validateResourceRequests will return a WARN when the resource request is not set
func validateResourceRequests(csv *operatorsv1alpha1.ClusterServiceVersion) (errs, warns []error) {
	if csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs == nil {
		errs = append(errs, goerrors.New("unable to find a deployment to install in the CSV"))
		return errs, warns
	}
	deploymentSpec := csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs

	for _, dSpec := range deploymentSpec {
		for _, c := range dSpec.Spec.Template.Spec.Containers {
			if c.Resources.Requests == nil || !(len(c.Resources.Requests.Cpu().String()) != 0 && len(c.Resources.Requests.Memory().String()) != 0) {
				msg := fmt.Errorf("unable to find the resource requests for the container: (%s). It is recommended "+
					"to ensure the resource request for CPU and Memory. Be aware that for some clusters configurations "+
					"it is required to specify requests or limits for those values. Otherwise, the system or quota may "+
					"reject Pod creation. More info: https://master.sdk.operatorframework.io/docs/best-practices/managing-resources/", c.Name)
				warns = append(warns, msg)
			}
		}
	}
	return errs, warns
}

// validateHubChannels will check the channels. The motivation for the following check is to ensure that operators
// authors knows if their operator bundles are or not respecting the Naming Convention Rules.
// However, the operator authors still able to choose the names as please them.
func validateHubChannels(channels []string) error {
	const candidate = "candidate"
	const stable = "stable"
	const fast = "fast"

	channels = getUniqueValues(channels)
	var channelsNotFollowingConventional []string
	for _, channel := range channels {
		if !strings.HasPrefix(channel, candidate) &&
			!strings.HasPrefix(channel, stable) &&
			!strings.HasPrefix(channel, fast) &&
			channel != "" {
			channelsNotFollowingConventional = append(channelsNotFollowingConventional, channel)
		}

	}

	if len(channelsNotFollowingConventional) > 0 {
		return fmt.Errorf("channel(s) %+q are not following the recommended naming convention: "+
			"https://olm.operatorframework.io/docs/best-practices/channel-naming",
			channelsNotFollowingConventional)
	}

	return nil
}

// getUniqueValues return the values without duplicates
func getUniqueValues(array []string) []string {
	var result []string
	uniqueValues := make(map[string]string)
	for _, n := range array {
		uniqueValues[strings.TrimSpace(n)] = ""
	}

	for k, _ := range uniqueValues {
		result = append(result, k)
	}
	return result
}

// validateCrdDescrptions ensures that all CRDs defined in the bundle have non-empty descriptions.
func validateCrdDescriptions(crds operatorsv1alpha1.CustomResourceDefinitions) []error {
	f := func(crds []operatorsv1alpha1.CRDDescription, relation string) []error {
		errors := make([]error, 0, len(crds))
		for _, crd := range crds {
			if crd.Description == "" {
				errors = append(errors, fmt.Errorf("%s CRD %q has an empty description", relation, crd.Name))
			}
		}
		return errors
	}

	return append(f(crds.Owned, "owned"), f(crds.Required, "required")...)
}
