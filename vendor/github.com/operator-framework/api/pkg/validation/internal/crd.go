package internal

import (
	"strings"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/runtime"
)

var scheme = runtime.NewScheme()

func init() {
	install.Install(scheme)
}

var CRDValidator interfaces.Validator = interfaces.ValidatorFunc(validateCRDs)

func validateCRDs(objs ...interface{}) (results []errors.ManifestResult) {
	for _, obj := range objs {
		switch v := obj.(type) {
		case *v1beta1.CustomResourceDefinition:
			results = append(results, validateV1Beta1CRD(v))
		case *v1.CustomResourceDefinition:
			results = append(results, validateV1CRD(v))
		}
	}
	return results
}

func validateV1Beta1CRD(crd *v1beta1.CustomResourceDefinition) (result errors.ManifestResult) {
	internalCRD := &apiextensions.CustomResourceDefinition{}
	v1beta1.SetObjectDefaults_CustomResourceDefinition(crd)
	err := scheme.Converter().Convert(crd, internalCRD, nil)
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting crd", err))
		return result
	}

	result = validateInternalCRD(internalCRD)
	return result
}

func validateV1CRD(crd *v1.CustomResourceDefinition) (result errors.ManifestResult) {
	internalCRD := &apiextensions.CustomResourceDefinition{}
	v1.SetObjectDefaults_CustomResourceDefinition(crd)
	err := scheme.Converter().Convert(crd, internalCRD, nil)
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting crd", err))
		return result
	}

	result = validateInternalCRD(internalCRD)
	return result
}

func validateInternalCRD(crd *apiextensions.CustomResourceDefinition) (result errors.ManifestResult) {
	errList := validation.ValidateCustomResourceDefinition(crd)
	for _, err := range errList {
		if !strings.Contains(err.Field, "openAPIV3Schema") && !strings.Contains(err.Field, "status") {
			result.Add(errors.NewError(errors.ErrorType(err.Type), err.Error(), err.Field, err.BadValue))
		}
	}

	if result.HasError() {
		result.Name = crd.GetName()
	}
	return result
}
