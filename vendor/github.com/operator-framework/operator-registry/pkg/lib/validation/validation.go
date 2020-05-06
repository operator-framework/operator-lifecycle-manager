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
)

// BundleValidator implements Validator to validate Bundles.
var BundleValidator = RegistryBundleValidator

// AllValidators implements Validator to validate all Operator manifest types.
var AllValidators = interfaces.Validators{
	BundleValidator,
}
