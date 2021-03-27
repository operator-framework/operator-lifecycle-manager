package internal

import (
	"fmt"
	"strings"

	v1 "github.com/operator-framework/api/pkg/operators/v1"
	"github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/api/pkg/validation/errors"
)

// CaseSensitiveAnnotationKeySet is a set of annotation keys that are case sensitive
// and can be used for validation purposes. The key is always lowercase and the value
// contains the expected case sensitive string. This may not be an exhaustive list.
var CaseSensitiveAnnotationKeySet = map[string]string{

	strings.ToLower(v1.OperatorGroupAnnotationKey):             v1.OperatorGroupAnnotationKey,
	strings.ToLower(v1.OperatorGroupNamespaceAnnotationKey):    v1.OperatorGroupNamespaceAnnotationKey,
	strings.ToLower(v1.OperatorGroupTargetsAnnotationKey):      v1.OperatorGroupTargetsAnnotationKey,
	strings.ToLower(v1.OperatorGroupProvidedAPIsAnnotationKey): v1.OperatorGroupProvidedAPIsAnnotationKey,
	strings.ToLower(v1alpha1.SkipRangeAnnotationKey):           v1alpha1.SkipRangeAnnotationKey,
}

/*
ValidateAnnotationNames will check annotation keys to ensure they are using
proper case. Uses CaseSensitiveAnnotationKeySet as a source for keys
which are known to be case sensitive. This function can be used anywhere
annotations need to be checked for case sensitivity.

Arguments

• annotations: annotations map usually obtained from ObjectMeta.GetAnnotations()

• value: is the field or file that caused an error or warning

Returns

• errs: Any errors that may have been detected with the annotation keys provided
*/
func ValidateAnnotationNames(annotations map[string]string, value interface{}) (errs []errors.Error) {
	// for every annotation provided
	for annotationKey := range annotations {
		// check the case sensitive key set for a matching lowercase annotation
		if knownCaseSensitiveKey, ok := CaseSensitiveAnnotationKeySet[strings.ToLower(annotationKey)]; ok {
			// we have a case-insensitive match... now check to see if the case is really correct
			if annotationKey != knownCaseSensitiveKey {
				// annotation key supplied is invalid due to bad case.
				errs = append(errs, errors.ErrFailedValidation(fmt.Sprintf("provided annotation %s uses wrong case and should be %s instead", annotationKey, knownCaseSensitiveKey), value))
			}
		}
	}
	return errs
}
