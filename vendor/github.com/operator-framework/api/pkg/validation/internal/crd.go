package internal

import (
	"strings"

	"github.com/operator-framework/api/pkg/validation/errors"
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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
	v1beta1.SetDefaults_CustomResourceDefinition(crd)
	v1beta1.SetDefaults_CustomResourceDefinitionSpec(&crd.Spec)
	err := scheme.Converter().Convert(crd, internalCRD, conversion.SourceToDest, nil)
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting crd", err))
		return result
	}

	gv := crd.GetObjectKind().GroupVersionKind().GroupVersion()
	result = validateInternalCRD(internalCRD, gv)
	return result
}

func validateV1CRD(crd *v1.CustomResourceDefinition) (result errors.ManifestResult) {
	internalCRD := &apiextensions.CustomResourceDefinition{}
	v1.SetDefaults_CustomResourceDefinition(crd)
	v1.SetDefaults_CustomResourceDefinitionSpec(&crd.Spec)
	err := scheme.Converter().Convert(crd, internalCRD, conversion.SourceToDest, nil)
	if err != nil {
		result.Add(errors.ErrInvalidParse("error converting crd", err))
		return result
	}

	gv := crd.GetObjectKind().GroupVersionKind().GroupVersion()
	result = validateInternalCRD(internalCRD, gv)
	return result
}

func validateInternalCRD(crd *apiextensions.CustomResourceDefinition, gv schema.GroupVersion) (result errors.ManifestResult) {
	errList := validation.ValidateCustomResourceDefinition(crd, gv)
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
