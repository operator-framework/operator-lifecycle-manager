package sat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/irifrance/gini"
)

// Identifier values uniquely identify particular Installables within
// the input to a single call to Solve.
type Identifier string

// Installable values are the basic unit of problems and solutions
// understood by this package.
type Installable interface {
	// Identifier returns the Identifier that uniquely identifies
	// this Installable among all other Installables in a given
	// problem.
	Identifier() Identifier
	// Constraints returns the set of constraints that apply to
	// this Installable.
	Constraints() []Constraint
}

var Incomplete = errors.New("cancelled before a solution could be found")

// NotSatisfiable is an error composed of a minimal set of applied
// constraints that is sufficient to make a solution impossible.
type NotSatisfiable []AppliedConstraint

func (e NotSatisfiable) Error() string {
	const msg = "constraints not satisfiable"
	if len(e) == 0 {
		return msg
	}
	s := make([]string, len(e))
	for i, a := range e {
		s[i] = a.String()
	}
	return fmt.Sprintf("%s: %s", msg, strings.Join(s, ", "))
}

const (
	satisfiable   = 1
	unsatisfiable = -1
)

// Solve takes a slice containing all Installables and returns a slice
// containing only those Installables that were selected for
// installation. The solution will attempt to minimize the total
// weight of all selected Installables. If no solution is possible, an
// error is returned.
func Solve(installables []Installable) ([]Installable, error) {
	return SolveWithContext(context.TODO(), installables)
}

// SolveWithContext provides the same behavior as Solve and may also
// return an error if the provided Context times out or is cancelled
// before a result is available.
func SolveWithContext(ctx context.Context, installables []Installable) (result []Installable, err error) {
	d := compileDict(installables)
	defer func() {
		// This likely indicates a bug, so discard whatever
		// return values were produced.
		if derr := d.Error(); derr != nil {
			result = nil
			err = derr
		}
	}()

	g := gini.New()
	switch d.Solve(ctx, g) {
	case satisfiable:
		var result []Installable
		for _, i := range installables {
			if g.Value(d.LitOf(i.Identifier())) {
				result = append(result, i)
			}
		}
		return result, nil
	case unsatisfiable:
		whys := g.Why(nil)
		as := make([]AppliedConstraint, len(whys))
		for i, why := range whys {
			as[i] = d.ConstraintOf(why)
		}
		return nil, NotSatisfiable(as)
	}

	return nil, Incomplete
}
