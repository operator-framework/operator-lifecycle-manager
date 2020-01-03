package bundle

import (
	"fmt"
	"strings"
)

// ValidationError is an imlementation of the Error type
// that defines a set of errors when validating the bundle
type ValidationError struct {
	AnnotationErrors []error
	FormatErrors     []error
}

func (v ValidationError) Error() string {
	var errs []string
	for _, err := range v.AnnotationErrors {
		errs = append(errs, err.Error())
	}
	for _, err := range v.FormatErrors {
		errs = append(errs, err.Error())
	}
	return fmt.Sprintf("Bundle validation errors: %s",
		strings.Join(errs, ","))
}

func NewValidationError(annotationErrs, formatErrs []error) ValidationError {
	return ValidationError{
		AnnotationErrors: annotationErrs,
		FormatErrors:     formatErrs,
	}
}
