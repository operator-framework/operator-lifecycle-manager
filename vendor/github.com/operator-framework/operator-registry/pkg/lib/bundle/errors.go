package bundle

import (
	"fmt"
	"strings"
)

// ValidationError is an imlementation of the Error type
// that defines a list of errors when validating the bundle
type ValidationError struct {
	Errors []error
}

func (v ValidationError) Error() string {
	var errs []string
	for _, err := range v.Errors {
		errs = append(errs, err.Error())
	}
	return fmt.Sprintf("Bundle validation errors: %s",
		strings.Join(errs, ","))
}

func NewValidationError(errs []error) ValidationError {
	return ValidationError{
		Errors: errs,
	}
}
