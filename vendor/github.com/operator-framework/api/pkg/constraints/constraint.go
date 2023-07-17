package constraints

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// OLMConstraintType is the schema "type" key for all constraints known to OLM
// (except for legacy types).
const OLMConstraintType = "olm.constraint"

// Constraint holds parsed, potentially nested dependency constraints.
type Constraint struct {
	// Constraint failure message that surfaces in resolution
	// This field is optional
	FailureMessage string `json:"failureMessage,omitempty" yaml:"failureMessage,omitempty"`

	// The cel struct that contraints CEL expression
	Cel *Cel `json:"cel,omitempty" yaml:"cel,omitempty"`

	// Package defines a constraint for a package within a version range.
	Package *PackageConstraint `json:"package,omitempty" yaml:"package,omitempty"`

	// GVK defines a constraint for a GVK.
	GVK *GVKConstraint `json:"gvk,omitempty" yaml:"gvk,omitempty"`

	// All, Any, and Not are compound constraints. See this enhancement for details:
	// https://github.com/operator-framework/enhancements/blob/master/enhancements/compound-bundle-constraints.md
	All *CompoundConstraint `json:"all,omitempty" yaml:"all,omitempty"`
	Any *CompoundConstraint `json:"any,omitempty" yaml:"any,omitempty"`
	// A note on Not: this constraint isn't particularly useful by itself.
	// It should be used within an All constraint alongside some other constraint type
	// since saying "do not use any of these GVKs/packages/etc." without an alternative
	// doesn't make sense.
	Not *CompoundConstraint `json:"not,omitempty" yaml:"not,omitempty"`
}

// CompoundConstraint holds a list of potentially nested constraints
// over which a boolean operation is applied.
type CompoundConstraint struct {
	Constraints []Constraint `json:"constraints" yaml:"constraints"`
}

// GVKConstraint defines a GVK constraint.
type GVKConstraint struct {
	Group   string `json:"group" yaml:"group"`
	Kind    string `json:"kind" yaml:"kind"`
	Version string `json:"version" yaml:"version"`
}

// PackageConstraint defines a package constraint.
type PackageConstraint struct {
	// PackageName is the name of the package.
	PackageName string `json:"packageName" yaml:"packageName"`
	// VersionRange required for the package.
	VersionRange string `json:"versionRange" yaml:"versionRange"`
}

// maxConstraintSize defines the maximum raw size in bytes of an olm.constraint.
// 64Kb seems reasonable, since this number allows for long description strings
// and either few deep nestings or shallow nestings and long constraints lists,
// but not both.
// QUESTION: make this configurable?
const maxConstraintSize = 2 << 16

// ErrMaxConstraintSizeExceeded is returned when a constraint's size > maxConstraintSize.
var ErrMaxConstraintSizeExceeded = fmt.Errorf("olm.constraint value is greater than max constraint size %d bytes", maxConstraintSize)

// Parse parses an olm.constraint property's value recursively into a Constraint.
// Unknown value schemas result in an error. Constraints that exceed the number of bytes
// defined by maxConstraintSize result results in an error.
func Parse(v json.RawMessage) (c Constraint, err error) {
	// There is no way to explicitly limit nesting depth.
	// From https://github.com/golang/go/issues/31789#issuecomment-538134396,
	// the recommended approach is to error out if raw input size
	// is greater than some threshold.
	if len(v) > maxConstraintSize {
		return c, ErrMaxConstraintSizeExceeded
	}

	d := json.NewDecoder(bytes.NewBuffer(v))
	d.DisallowUnknownFields()
	err = d.Decode(&c)

	return
}
