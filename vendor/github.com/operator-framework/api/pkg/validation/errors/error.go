package errors

import (
	"fmt"
)

// ManifestResult represents verification result for each of the yaml files
// from the operator manifest.
type ManifestResult struct {
	// Name is some piece of information identifying the manifest.
	Name string
	// Errors pertain to issues with the manifest that must be corrected.
	Errors []Error
	// Warnings pertain to issues with the manifest that are optional to correct.
	Warnings []Error
}

// Add appends errs to r in either r.Errors or r.Warnings depending on an
// error's Level.
func (r *ManifestResult) Add(errs ...Error) {
	for _, err := range errs {
		if err.Level == LevelError {
			r.Errors = append(r.Errors, err)
		} else {
			r.Warnings = append(r.Warnings, err)
		}
	}
}

// HasError returns true if r has any Errors of Level == LevelError.
func (r ManifestResult) HasError() bool {
	return len(r.Errors) != 0
}

// HasWarn returns true if r has any Errors of Level == LevelWarn.
func (r ManifestResult) HasWarn() bool {
	return len(r.Warnings) != 0
}

// QUESTION: use field.Error instead of our own implementation? seems like most
// of what we want is already in the field package:
// https://godoc.org/k8s.io/apimachinery/pkg/util/validation/field

// Error is an implementation of the 'error' interface, which represents a
// warning or an error in a yaml file.
type Error struct {
	// Type is the ErrorType string constant that represents the kind of
	// error, ex. "MandatoryStructMissing", "I/O".
	Type ErrorType
	// Level is the severity of the Error.
	Level Level
	// Field is the dot-hierarchical YAML path of the missing data.
	Field string
	// BadValue is the field or file that caused an error or warning.
	BadValue interface{}
	// Detail represents the error message as a string.
	Detail string
}

// Error implements the 'error' interface to define custom error formatting.
func (e Error) Error() string {
	detail := e.Detail
	if detail != "" {
		detail = fmt.Sprintf(": %s", detail)
	}
	if e.Field != "" && e.BadValue != nil {
		detail = fmt.Sprintf("Field %s, Value %v%s", e.Field, e.BadValue, detail)
	} else if e.Field != "" {
		detail = fmt.Sprintf("Field %s%s", e.Field, detail)
	} else if e.BadValue != nil {
		detail = fmt.Sprintf("Value %v%s", e.BadValue, detail)
	}
	if detail != "" {
		return fmt.Sprintf("%s: %s", e.Level, detail)
	}
	return "ErrMessageMissing"
}

// Level is the severity of an Error.
type Level string

const (
	// LevelWarn is for Errors that should be addressed but do not have to be.
	LevelWarn = "Warning"
	// LevelError is for Errors that must be addressed.
	LevelError = "Error"
)

// ErrorType defines what the error resulted from.
type ErrorType string

const (
	ErrorInvalidCSV               ErrorType = "CSVFileNotValid"
	ErrorFieldMissing             ErrorType = "FieldNotFound"
	ErrorUnsupportedType          ErrorType = "FieldTypeNotSupported"
	ErrorInvalidParse             ErrorType = "ParseError"
	ErrorIO                       ErrorType = "FileReadError"
	ErrorFailedValidation         ErrorType = "ValidationFailed"
	ErrorInvalidOperation         ErrorType = "OperationFailed"
	ErrorInvalidManifestStructure ErrorType = "ManifestStructureNotValid"
	ErrorInvalidBundle            ErrorType = "BundleNotValid"
	ErrorInvalidPackageManifest   ErrorType = "PackageManifestNotValid"
	ErrorObjectFailedValidation   ErrorType = "ObjectFailedValidation"
)

func NewError(t ErrorType, detail, field string, v interface{}) Error {
	return Error{t, LevelError, field, v, detail}
}

func NewWarn(t ErrorType, detail, field string, v interface{}) Error {
	return Error{t, LevelWarn, field, v, detail}
}

func ErrInvalidBundle(detail string, value interface{}) Error {
	return invalidBundle(LevelError, detail, value)
}

func WarnInvalidBundle(detail string, value interface{}) Error {
	return invalidBundle(LevelError, detail, value)
}

func invalidBundle(lvl Level, detail string, value interface{}) Error {
	return Error{ErrorInvalidBundle, lvl, "", value, detail}
}

func ErrInvalidManifestStructure(detail string) Error {
	return invalidManifestStructure(LevelError, detail)
}

func WarnInvalidManifestStructure(detail string) Error {
	return invalidManifestStructure(LevelWarn, detail)
}

func invalidManifestStructure(lvl Level, detail string) Error {
	return Error{ErrorInvalidManifestStructure, lvl, "", "", detail}
}

func ErrInvalidCSV(detail, csvName string) Error {
	return invalidCSV(LevelError, detail, csvName)
}

func WarnInvalidCSV(detail, csvName string) Error {
	return invalidCSV(LevelWarn, detail, csvName)
}

func invalidCSV(lvl Level, detail, csvName string) Error {
	return Error{ErrorInvalidCSV, lvl, "", "", fmt.Sprintf("(%s) %s", csvName, detail)}
}

func ErrFieldMissing(detail string, field string, value interface{}) Error {
	return fieldMissing(LevelError, detail, field, value)
}

func WarnFieldMissing(detail string, field string, value interface{}) Error {
	return fieldMissing(LevelWarn, detail, field, value)
}

func fieldMissing(lvl Level, detail string, field string, value interface{}) Error {
	return Error{ErrorFieldMissing, lvl, field, value, detail}
}

func ErrUnsupportedType(detail string) Error {
	return unsupportedType(LevelError, detail)
}

func WarnUnsupportedType(detail string) Error {
	return unsupportedType(LevelWarn, detail)
}

func unsupportedType(lvl Level, detail string) Error {
	return Error{ErrorUnsupportedType, lvl, "", "", detail}
}

// TODO: see if more information can be extracted out of 'unmarshall/parsing' errors.
func ErrInvalidParse(detail string, value interface{}) Error {
	return invalidParse(LevelError, detail, value)
}

func WarnInvalidParse(detail string, value interface{}) Error {
	return invalidParse(LevelWarn, detail, value)
}

func invalidParse(lvl Level, detail string, value interface{}) Error {
	return Error{ErrorInvalidParse, lvl, "", value, detail}
}

func ErrInvalidPackageManifest(detail string, pkgName string) Error {
	return invalidPackageManifest(LevelError, detail, pkgName)
}

func WarnInvalidPackageManifest(detail string, pkgName string) Error {
	return invalidPackageManifest(LevelWarn, detail, pkgName)
}

func invalidPackageManifest(lvl Level, detail string, pkgName string) Error {
	return Error{ErrorInvalidPackageManifest, lvl, "", "", fmt.Sprintf("(%s) %s", pkgName, detail)}
}

func ErrIOError(detail string, value interface{}) Error {
	return iOError(LevelError, detail, value)
}

func WarnIOError(detail string, value interface{}) Error {
	return iOError(LevelWarn, detail, value)
}

func iOError(lvl Level, detail string, value interface{}) Error {
	return Error{ErrorIO, lvl, "", value, detail}
}

func ErrFailedValidation(detail string, value interface{}) Error {
	return failedValidation(LevelError, detail, value)
}

func WarnFailedValidation(detail string, value interface{}) Error {
	return failedValidation(LevelWarn, detail, value)
}

func failedValidation(lvl Level, detail string, value interface{}) Error {
	return Error{ErrorFailedValidation, lvl, "", value, detail}
}

func ErrInvalidOperation(detail string, value interface{}) Error {
	return invalidOperation(LevelError, detail, value)
}

func WarnInvalidOperation(detail string, value interface{}) Error {
	return invalidOperation(LevelWarn, detail, value)
}

func invalidOperation(lvl Level, detail string, value interface{}) Error {
	return Error{ErrorInvalidOperation, lvl, "", value, detail}
}

func ErrInvalidObject(value interface{}, detail string) Error {
	return invalidObject(LevelError, detail, value)
}

func invalidObject(lvl Level, detail string, value interface{}) Error {
	return Error{ErrorObjectFailedValidation, lvl, "", value, detail}
}

func WarnInvalidObject(detail string, value interface{}) Error {
	return failedValidation(LevelWarn, detail, value)
}

func WarnMissingIcon(detail string) Error {
	return failedValidation(LevelWarn, detail, "")
}
