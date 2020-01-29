package validator

import (
	"github.com/operator-framework/api/pkg/validation/errors"
)

// Validator is an interface for validating arbitrary objects.
type Validator interface {
	// Validate takes a list of arbitrary objects and returns a slice of results,
	// one for each object validated.
	Validate(...interface{}) []errors.ManifestResult
	// WithValidators returns a Validator appended to a variable number of
	// Validator's.
	WithValidators(...Validator) Validators
}

// ValidatorFunc implements Validator. ValidatorFunc can be used as a wrapper
// for functions that run object validators.
type ValidatorFunc func(...interface{}) []errors.ManifestResult

// Validate runs the ValidatorFunc on objs.
func (f ValidatorFunc) Validate(objs ...interface{}) (results []errors.ManifestResult) {
	return f(objs...)
}

// WithValidators appends the ValidatorFunc to vals.
func (f ValidatorFunc) WithValidators(vals ...Validator) Validators {
	return append(vals, f)
}

// Validators is a set of Validator's that implements Validate.
type Validators []Validator

// Validate invokes each Validator in Validators, collecting and returning
// the results.
func (validators Validators) Validate(objs ...interface{}) (results []errors.ManifestResult) {
	for _, validator := range validators {
		results = append(results, validator.Validate(objs...)...)
	}
	return results
}

// WithValidators appends vals to Validators.
func (validators Validators) WithValidators(vals ...Validator) Validators {
	return append(vals, validators...)
}
