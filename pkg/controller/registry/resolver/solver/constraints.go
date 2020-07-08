package solver

import (
	"fmt"
	"strings"
)

// constrainer is a reusable accumulator of constraint clause terms.
type constrainer struct {
	pos []Identifier
	neg []Identifier
}

func (x *constrainer) Add(id Identifier) {
	x.pos = append(x.pos, id)
}

func (x *constrainer) AddNot(id Identifier) {
	x.neg = append(x.neg, id)
}

// Reset clears the receiver's internal state so that it can be
// reused.
func (x *constrainer) Reset() {
	x.pos = x.pos[:0]
	x.neg = x.neg[:0]
}

// Empty returns true if and only if the receiver has accumulated no
// positive or negative terms.
func (x *constrainer) Empty() bool {
	return len(x.pos) == 0 && len(x.neg) == 0
}

// Constraint implementations limit the circumstances under which a
// particular Installable can appear in a solution.
type Constraint interface {
	String(subject Identifier) string
	apply(x *constrainer, subject Identifier)
	order() []Identifier
}

// zeroConstraint is returned by ConstraintOf in error cases.
type zeroConstraint struct{}

var _ Constraint = zeroConstraint{}

func (zeroConstraint) String(subject Identifier) string {
	return ""
}

func (zeroConstraint) apply(x *constrainer, subject Identifier) {
}

func (zeroConstraint) order() []Identifier {
	return nil
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

type mandatory struct{}

func (c mandatory) String(subject Identifier) string {
	return fmt.Sprintf("%s is mandatory", subject)
}

func (c mandatory) apply(x *constrainer, subject Identifier) {
	x.Add(subject)
}

func (c mandatory) order() []Identifier {
	return nil
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

func (c prohibited) apply(x *constrainer, subject Identifier) {
	x.AddNot(subject)
}

func (c prohibited) order() []Identifier {
	return nil
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

func (c dependency) apply(x *constrainer, subject Identifier) {
	if len(c) == 0 {
		return
	}
	x.AddNot(subject)
	for _, each := range c {
		x.Add(each)
	}
}

func (c dependency) order() []Identifier {
	return []Identifier(c)
}

// Dependency returns a Constraint that will only permit solutions
// containing a given Installable on the condition that at least one
// of the Installables identified by the given Identifiers also
// appears in the solution. Identifiers appearing earlier in the
// argument list have higher preference than those appearing later.
func Dependency(ids ...Identifier) Constraint {
	return dependency(ids)
}

type conflict Identifier

func (c conflict) String(subject Identifier) string {
	return fmt.Sprintf("%s conflicts with %s", subject, c)
}

func (c conflict) apply(x *constrainer, subject Identifier) {
	x.AddNot(subject)
	x.AddNot(Identifier(c))
}

func (c conflict) order() []Identifier {
	return nil
}

// Conflict returns a Constraint that will permit solutions containing
// either the constrained Installable, the Installable identified by
// the given Identifier, or neither, but not both.
func Conflict(id Identifier) Constraint {
	return conflict(id)
}
