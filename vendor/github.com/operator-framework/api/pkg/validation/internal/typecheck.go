package internal

import (
	"encoding/json"
	"fmt"
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
		emptyVal := isEmptyValue(fieldValue)

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

// Uses reflect package to check if the value of the object passed is null, returns a boolean accordingly.
// TODO: replace with reflect.Kind.IsZero() in go 1.13
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		// Check if the value for 'Spec.InstallStrategy.StrategySpecRaw' field is present. This field is a RawMessage value type. Without a value, the key is explicitly set to 'null'.
		if fieldValue, ok := v.Interface().(json.RawMessage); ok {
			valString := string(fieldValue)
			if valString == "null" {
				return true
			}
		}
		return v.Len() == 0
	// Currently the only CSV field with integer type is containerPort. Operator Verification Library raises a warning if containerPort field is missisng or if its value is 0.
	// It is an optional field so the user can ignore the warning saying this field is missing if they intend to use port 0.
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	case reflect.Struct:
		for i, n := 0, v.NumField(); i < n; i++ {
			if !isEmptyValue(v.Field(i)) {
				return false
			}
		}
		return true
	default:
		panic(fmt.Sprintf("%v kind is not supported.", v.Kind()))
	}
}
