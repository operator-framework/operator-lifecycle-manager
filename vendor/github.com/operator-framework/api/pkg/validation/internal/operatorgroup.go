package internal

import (
	operatorsv1 "github.com/operator-framework/api/pkg/operators/v1"
	operatorsv1alpha2 "github.com/operator-framework/api/pkg/operators/v1alpha2"
	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
)

// OperatorGroupValidator is a validator for OperatorGroup
var OperatorGroupValidator interfaces.Validator = interfaces.ValidatorFunc(validateOperatorGroups)

func validateOperatorGroups(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *operatorsv1.OperatorGroup:
			results = append(results, validateOperatorGroupV1(v))
		case *operatorsv1alpha2.OperatorGroup:
			results = append(results, validateOperatorGroupV1Alpha2(v))
		}
	}
	return results
}

func validateOperatorGroupV1Alpha2(operatorGroup *operatorsv1alpha2.OperatorGroup) (result errors.ManifestResult) {
	// validate case sensitive annotation names
	result.Add(ValidateAnnotationNames(operatorGroup.GetAnnotations(), operatorGroup.GetName())...)
	return result
}

func validateOperatorGroupV1(operatorGroup *operatorsv1.OperatorGroup) (result errors.ManifestResult) {
	// validate case sensitive annotation names
	result.Add(ValidateAnnotationNames(operatorGroup.GetAnnotations(), operatorGroup.GetName())...)
	return result
}
