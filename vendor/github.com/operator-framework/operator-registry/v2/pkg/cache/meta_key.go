package cache

import (
	"fmt"
	"regexp"
)

// ValidationError is returned by ListPackageCustomSchemas for invalid schema or packageName inputs.
type ValidationError struct {
	Err error
}

func (e *ValidationError) Error() string { return e.Err.Error() }
func (e *ValidationError) Unwrap() error { return e.Err }

// validMetaKeyComponent guards against path traversal — not Windows filesystem quirks.
// opm serve targets Linux containers; Windows-specific concerns (trailing dots, reserved
// device names) are out of scope.
var validMetaKeyComponent = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

func validateMetaKeyComponent(name, value string) error {
	if value == "" {
		return &ValidationError{fmt.Errorf("invalid %s: must not be empty", name)}
	}
	if !validMetaKeyComponent.MatchString(value) {
		return &ValidationError{fmt.Errorf("invalid %s %q: must match %s", name, value, validMetaKeyComponent.String())}
	}
	return nil
}

func newValidatedMetaKey(schema, packageName string) (metaKey, error) {
	if err := validateMetaKeyComponent("schema", schema); err != nil {
		return metaKey{}, err
	}
	if packageName != "" {
		if err := validateMetaKeyComponent("packageName", packageName); err != nil {
			return metaKey{}, err
		}
	}
	return metaKey{Schema: schema, PackageName: packageName}, nil
}

type metaKey struct {
	Schema      string
	PackageName string
}
