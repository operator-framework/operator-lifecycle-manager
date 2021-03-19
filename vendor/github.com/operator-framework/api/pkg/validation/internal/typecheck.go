package internal

import (
	"reflect"
	"strings"

	"github.com/operator-framework/api/pkg/validation/errors"
)

// Recursive function that traverses a nested struct passed in as reflect
// value, and reports for errors/warnings in case of nil struct field values.
// TODO: make iterative
func checkEmptyFields(result *errors.ManifestResult, v reflect.Value, parentStructName string) {
	if v.Kind() != reflect.Struct {
		return
	}
	typ := v.Type()

	for i := 0; i < v.NumField(); i++ {
		fieldValue := v.Field(i)
		fieldType := typ.Field(i)

		tag := fieldType.Tag.Get("json")
		// Ignore fields that are subsets of a primitive field.
		if tag == "" {
			continue
		}

		// Omitted field tags will contain ",omitempty", and ignored tags will
		// match "-" exactly, respectively.
		isOptionalField := strings.Contains(tag, ",omitempty") || tag == "-"
		emptyVal := fieldValue.IsZero()

		newParentStructName := fieldType.Name
		if parentStructName != "" {
			newParentStructName = parentStructName + "." + newParentStructName
		}

		switch fieldValue.Kind() {
		case reflect.Struct:
			updateResult(result, "struct", newParentStructName, emptyVal, isOptionalField)
			if !emptyVal {
				checkEmptyFields(result, fieldValue, newParentStructName)
			}
		default:
			updateResult(result, "field", newParentStructName, emptyVal, isOptionalField)
		}
	}
}

// Returns updated ManifestResult with missing optional/mandatory field/struct objects.
func updateResult(result *errors.ManifestResult, typeName string, newParentStructName string, emptyVal bool, isOptionalField bool) {
	if !emptyVal {
		return
	}
	if !isOptionalField && newParentStructName != "Status" {
		result.Add(errors.ErrFieldMissing("required field missing", newParentStructName, typeName))
	}
}
