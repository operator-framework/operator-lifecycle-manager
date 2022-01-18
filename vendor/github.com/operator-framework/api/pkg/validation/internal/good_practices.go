package internal

import (
	goerrors "errors"
	"fmt"

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
