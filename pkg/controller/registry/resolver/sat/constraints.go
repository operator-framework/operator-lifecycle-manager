package sat

import (
	"fmt"
	"strings"
)

// Constraint implementations limit the circumstances under which a
// particular Installable can appear in a solution.
type Constraint interface {
	String(subject Identifier) string
	apply(x constrainer, subject Identifier)
}

// AppliedConstraint values compose a single Constraint with the
// Installable it applies to.
type AppliedConstraint struct {
	Installable Installable
	Constraint  Constraint
}

// String implements fmt.Stringer and returns a human-readable message
// representing the receiver.
func (a AppliedConstraint) String() string {
	return a.Constraint.String(a.Installable.Identifier())
}

// constrainer is the set of operations available to Constraint
// implementations.
type constrainer interface {
	// Add appends the Installable identified by the given
	// Identifier to the clause representing a Constraint.
	Add(Identifier)
	// Add appends the negation of the Installable identified by
	// the given Identifier to the clause representing a
	// Constraint.
	AddNot(Identifier)
	// Weight sets an additional weight to add to the constrained
	// Installable. Calls with negative arguments are ignored.
	Weight(int)
}

type mandatory struct{}

func (c mandatory) String(subject Identifier) string {
	return fmt.Sprintf("%s is mandatory", subject)
}

func (c mandatory) apply(x constrainer, subject Identifier) {
	x.Add(subject)
}

// Mandatory returns a Constraint that will permit only solutions that
// contain a particular Installable.
func Mandatory() Constraint {
	return mandatory{}
}

type prohibited struct{}

func (c prohibited) String(subject Identifier) string {
	return fmt.Sprintf("%s is prohibited", subject)
}

func (c prohibited) apply(x constrainer, subject Identifier) {
	x.AddNot(subject)
}

// Prohibited returns a Constraint that will reject any solution that
// contains a particular Installable. Callers may also decide to omit
// an Installable from input to Solve rather than apply such a
// Constraint.
func Prohibited() Constraint {
	return prohibited{}
}

type dependency []Identifier

func (c dependency) String(subject Identifier) string {
	s := make([]string, len(c))
	for i, each := range c {
		s[i] = string(each)
	}
	return fmt.Sprintf("%s requires at least one of %s", subject, strings.Join(s, ", "))
}

func (c dependency) apply(x constrainer, subject Identifier) {
	if len(c) == 0 {
		return
	}
	x.AddNot(subject)
	for _, each := range c {
		x.Add(each)
	}
}

// Dependency returns a Constraint that will only permit solutions
// containing a given Installable on the condition that at least one
// of the Installables identified by the given Identifiers also
// appears in the solution.
func Dependency(ids ...Identifier) Constraint {
	return dependency(ids)
}

type conflict Identifier

func (c conflict) String(subject Identifier) string {
	return fmt.Sprintf("%s conflicts with %s", subject, c)
}

func (c conflict) apply(x constrainer, subject Identifier) {
	x.AddNot(subject)
	x.AddNot(Identifier(c))
}

// Conflict returns a Constraint that will permit solutions containing
// either the constrained Installable, the Installable identified by
// the given Identifier, or neither, but not both.
func Conflict(id Identifier) Constraint {
	return conflict(id)
}

type weight int

func (c weight) String(subject Identifier) string {
	return fmt.Sprintf("%s has weight %d", subject, c)
}

func (c weight) apply(x constrainer, subject Identifier) {
	x.Weight(int(c))
}

// Weight returns a Constraint that increases the weight of the
// constrainted Installable by the given (non-negative) amount.
func Weight(w int) Constraint {
	return weight(w)
}
