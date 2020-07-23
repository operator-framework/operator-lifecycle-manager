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
var OperatorHubValidator = internal.OperatorHubValidator

// AllValidators implements Validator to validate all Operator manifest types.
var AllValidators = interfaces.Validators{
	PackageManifestValidator,
	ClusterServiceVersionValidator,
	CustomResourceDefinitionValidator,
	BundleValidator,
	OperatorHubValidator,
}

var DefaultBundleValidators = interfaces.Validators{
	ClusterServiceVersionValidator,
	CustomResourceDefinitionValidator,
	BundleValidator,
}
