// Package validation provides default Validator's that can be run with a list
// of arbitrary objects. The defaults exposed here consist of all Validator's
// implemented by this validation library.
//
// Each default Validator runs an independent set of validation functions on
// a set of objects. To run all implemented Validator's, use AllValidators.
// The Validator will not be run on objects of an inappropriate type.

package validation

import (
	interfaces "github.com/operator-framework/api/pkg/validation/interfaces"
	"github.com/operator-framework/api/pkg/validation/internal"
)

// PackageManifestValidator implements Validator to validate package manifests.
var PackageManifestValidator = internal.PackageManifestValidator

// ClusterServiceVersionValidator implements Validator to validate
// ClusterServiceVersions.
var ClusterServiceVersionValidator = internal.CSVValidator

// CustomResourceDefinitionValidator implements Validator to validate
// CustomResourceDefinitions.
var CustomResourceDefinitionValidator = internal.CRDValidator

// BundleValidator implements Validator to validate Bundles.
var BundleValidator = internal.BundleValidator

// OperatorHubValidator implements Validator to validate bundle objects
// for OperatorHub.io requirements.
//
// Deprecated: Use OperatorHubV2Validator, StandardCapabilitiesValidator
// and StandardCategoriesValidator for equivalent functionality.
var OperatorHubValidator = internal.OperatorHubValidator

// OperatorHubV2Validator implements Validator to validate bundle objects
// for OperatorHub.io requirements.
var OperatorHubV2Validator = internal.OperatorHubV2Validator

// StandardCapabilitiesValidator implements Validator to validate bundle objects
// for OperatorHub.io requirements around UI capability metadata
var StandardCapabilitiesValidator = internal.StandardCapabilitiesValidator

// StandardCategoriesValidator implements Validator to validate bundle objects
// for OperatorHub.io requirements around UI category metadata
var StandardCategoriesValidator = internal.StandardCategoriesValidator

// Object Validator validates various custom objects in the bundle like PDBs and SCCs.
// Object validation is optional and not a default-level validation.
var ObjectValidator = internal.ObjectValidator

// OperatorGroupValidator implements Validator to validate OperatorGroup manifests
var OperatorGroupValidator = internal.OperatorGroupValidator

// CommunityOperatorValidator implements Validator to validate bundle objects
// for the Community Operator requirements.
//
// Deprecated - The checks made for this validator were moved to the external one:
// https://github.com/redhat-openshift-ecosystem/ocp-olm-catalog-validator.
// Please no longer use this check it will be removed in the next releases.
var CommunityOperatorValidator = internal.CommunityOperatorValidator

// AlphaDeprecatedAPIsValidator implements Validator to validate bundle objects
// for API deprecation requirements.
//
// Note that this validator looks at the manifests. If any removed APIs for the mapped k8s versions are found,
// it raises a warning.
//
// This validator only raises an error when the deprecated API found is removed in the specified k8s
// version informed via the optional key `k8s-version`.
var AlphaDeprecatedAPIsValidator = internal.AlphaDeprecatedAPIsValidator

// GoodPracticesValidator implements Validator to validate the criteria defined as good practices
var GoodPracticesValidator = internal.GoodPracticesValidator

// MultipleArchitecturesValidator implements Validator to validate MultipleArchitectures configuration. For further
// information check: https://olm.operatorframework.io/docs/advanced-tasks/ship-operator-supporting-multiarch/
var MultipleArchitecturesValidator = internal.MultipleArchitecturesValidator

// AllValidators implements Validator to validate all Operator manifest types.
var AllValidators = interfaces.Validators{
	PackageManifestValidator,
	ClusterServiceVersionValidator,
	CustomResourceDefinitionValidator,
	BundleValidator,
	OperatorHubV2Validator,
	StandardCategoriesValidator,
	StandardCapabilitiesValidator,
	ObjectValidator,
	OperatorGroupValidator,
	CommunityOperatorValidator,
	AlphaDeprecatedAPIsValidator,
	GoodPracticesValidator,
	MultipleArchitecturesValidator,
}

var DefaultBundleValidators = interfaces.Validators{
	ClusterServiceVersionValidator,
	CustomResourceDefinitionValidator,
	BundleValidator,
}
