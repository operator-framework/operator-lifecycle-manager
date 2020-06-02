package sat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/irifrance/gini"
	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/logic"
	"github.com/irifrance/gini/z"
)

// dict performs translation between the input and output types of
// Solve (Constraints, Installables, etc.) and the variables that
// appear in the SAT formula.
type dict struct {
	installables map[z.Lit]Installable
	lits         map[Identifier]z.Lit
	constraints  map[z.Lit]AppliedConstraint
	c            *logic.C
	cards        *logic.CardSort
	errs         dictError
}

// cstate is a reusable constrainer that accumulates terms of
// constraint clauses.
type cstate struct {
	pos    []Identifier
	neg    []Identifier
	weight int
}

func (x *cstate) Add(id Identifier) {
	x.pos = append(x.pos, id)
}

func (x *cstate) AddNot(id Identifier) {
	x.neg = append(x.neg, id)
}

func (x *cstate) Weight(w int) {
	if w >= 0 {
		x.weight = w
	}
}

// Reset clears the receiver's internal state so that it can be
// reused.
func (x *cstate) Reset() {
	x.pos = x.pos[:0]
	x.neg = x.neg[:0]
	x.weight = 0
}

// Empty returns true if and only if the receiver has accumulated no
// positive or negative terms.
func (x *cstate) Empty() bool {
	return len(x.pos) == 0 && len(x.neg) == 0
}

type dictError []error

func (dictError) Error() string {
	return "internal solver failure"
}

// LitOf returns the positive literal corresponding to the Installable
// with the given Identifier.
func (d *dict) LitOf(id Identifier) z.Lit {
	m, ok := d.lits[id]
	if ok {
		return m
	}
	d.errs = append(d.errs, fmt.Errorf("installable %q referenced but not provided", id))
	return z.LitNull
}

// zeroInstallable is returned by InstallableOf in error cases.
type zeroInstallable struct{}

func (zeroInstallable) Identifier() Identifier {
	return ""
}

func (zeroInstallable) Constraints() []Constraint {
	return nil
}

// InstallableOf returns the Installable corresponding to the provided
// literal, or a zeroInstallable if no such Installable exists.
func (d *dict) InstallableOf(m z.Lit) Installable {
	i, ok := d.installables[m]
	if ok {
		return i
	}
	d.errs = append(d.errs, fmt.Errorf("no installable corresponding to %s", m))
	return zeroInstallable{}
}

// zeroConstraint is returned by ConstraintOf in error cases.
type zeroConstraint struct{}

func (zeroConstraint) String(subject Identifier) string {
	return ""
}

func (zeroConstraint) apply(x constrainer, subject Identifier) {
}

// ConstraintOf returns the constraint application corresponding to
// the provided literal, or a zeroConstraint if no such constraint
// exists.
func (d *dict) ConstraintOf(m z.Lit) AppliedConstraint {
	if a, ok := d.constraints[m]; ok {
		return a
	}
	d.errs = append(d.errs, fmt.Errorf("no constraint corresponding to %s", m))
	return AppliedConstraint{
		Installable: zeroInstallable{},
		Constraint:  zeroConstraint{},
	}
}

// Error returns a single error value that is an aggregation of all
// errors encountered during a dict's lifetime, or nil if there have
// been no errors. A non-nil return value likely indicates a problem
// with the solver or constraint implementations.
func (d *dict) Error() error {
	if len(d.errs) == 0 {
		return nil
	}
	s := make([]string, len(d.errs))
	for i, err := range d.errs {
		s[i] = err.Error()
	}
	return fmt.Errorf("%d errors encountered: %s", len(s), strings.Join(s, ", "))
}

// compileDict returns a new dict with its state initialized based on
// the provided slice of Installables. This includes construction of
// the translation tables between Installables/Constraints and the
// inputs to the underlying solver.
func compileDict(installables []Installable) *dict {
	d := dict{
		installables: make(map[z.Lit]Installable, len(installables)),
		lits:         make(map[Identifier]z.Lit, len(installables)),
		constraints:  make(map[z.Lit]AppliedConstraint),
		c:            logic.NewC(),
	}

	var buf []z.Lit
	var x cstate
	weights := make([]z.Lit, 0, len(installables))

	// First pass to assign lits from the circuit:
	for _, installable := range installables {
		im := d.c.Lit()
		d.installables[im] = installable
		d.lits[installable.Identifier()] = im
	}

	// Then build the constraints:
	for _, installable := range installables {
		for _, constraint := range installable.Constraints() {
			x.Reset()
			constraint.apply(&x, installable.Identifier())

			clauses := make([]z.Lit, 0, x.weight)
			for w := 0; w < x.weight; w++ {
				weights = append(weights, d.lits[installable.Identifier()])
			}

			if !x.Empty() {
				buf = buf[:0]
				for _, p := range x.pos {
					buf = append(buf, d.LitOf(p))
				}
				for _, n := range x.neg {
					buf = append(buf, d.LitOf(n).Not())
				}
				clauses = append(clauses, d.c.Ors(buf...))
			}
			m := d.c.Ands(clauses...)

			d.constraints[m] = AppliedConstraint{
				Installable: installable,
				Constraint:  constraint,
			}
		}
	}

	d.cards = d.c.CardSort(weights)

	return &d
}

// Solve searches for a solution to the problem encoded in a dict that
// minimizes the total weight of all selected Installables.
func (d *dict) Solve(ctx context.Context, g *gini.Gini) int {
	d.c.ToCnf(g)

	var result int
	linearSearch(0, d.cards.N(), func(w int) bool {
		if w >= 0 {
			// Omitting the cardinality constraint on the
			// last iteration prevents the cardinality
			// constraint literal from showing up in the
			// set of failed assumptions should there be
			// no solution.
			g.Assume(d.cards.Leq(w))
		}
		for m := range d.constraints {
			g.Assume(m)
		}
		result = waitForSolution(ctx, g.GoSolve())
		return result == satisfiable
	})

	return result
}

func waitForSolution(ctx context.Context, gs inter.Solve) int {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return gs.Stop()
		case <-t.C:
			if result, ok := gs.Test(); ok {
				return result
			}
		}
	}
}

func linearSearch(min, max int, f func(int) bool) {
	for x := min; x <= max; x++ {
		if f(x) {
			return
		}
	}
	f(-1) // omit cardinality constraint
}

func binarySearch(min, max int, f func(int) bool) {
	for {
		x := min + ((max - min) / 2)
		s := f(x)
		if min >= max {
			if !s {
				f(-1) // omit cardinality constraint
			}
			return
		}
		if s {
			max = x
		} else {
			min = x + 1
		}
	}
}
