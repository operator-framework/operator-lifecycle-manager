package solver

import (
	"fmt"
	"strings"

	"github.com/irifrance/gini"
	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/logic"
	"github.com/irifrance/gini/z"
)

// dict performs translation between the input and output types of
// Solve (Constraints, Installables, etc.) and the variables that
// appear in the SAT formula.
type dict struct {
	inorder      []Installable
	installables map[z.Lit]Installable
	lits         map[Identifier]z.Lit
	constraints  map[z.Lit]AppliedConstraint
	c            *logic.C
	errs         dictError
	buf          []z.Lit
}

// cstate is a reusable constrainer that accumulates terms of
// constraint clauses.
type cstate struct {
	pos []Identifier
	neg []Identifier
}

func (x *cstate) Add(id Identifier) {
	x.pos = append(x.pos, id)
}

func (x *cstate) AddNot(id Identifier) {
	x.neg = append(x.neg, id)
}

// Reset clears the receiver's internal state so that it can be
// reused.
func (x *cstate) Reset() {
	x.pos = x.pos[:0]
	x.neg = x.neg[:0]
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

func (zeroConstraint) order() []Identifier {
	return nil
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
		inorder:      installables,
		installables: make(map[z.Lit]Installable, len(installables)),
		lits:         make(map[Identifier]z.Lit, len(installables)),
		constraints:  make(map[z.Lit]AppliedConstraint),
		c:            logic.NewCCap(len(installables)),
	}

	// First pass to assign lits:
	for _, installable := range installables {
		im := d.c.Lit()
		d.lits[installable.Identifier()] = im
		d.installables[im] = installable
	}

	var x cstate
	for _, installable := range installables {
		for _, constraint := range installable.Constraints() {
			x.Reset()
			constraint.apply(&x, installable.Identifier())
			if x.Empty() {
				// This constraint doesn't have a
				// useful representation in the SAT
				// inputs.
				continue
			}

			d.buf = d.buf[:0]
			for _, p := range x.pos {
				d.buf = append(d.buf, d.LitOf(p))
			}
			for _, n := range x.neg {
				d.buf = append(d.buf, d.LitOf(n).Not())
			}
			m := d.c.Ors(d.buf...)

			d.constraints[m] = AppliedConstraint{
				Installable: installable,
				Constraint:  constraint,
			}
		}
	}

	return &d
}

func (d *dict) AddConstraints(g *gini.Gini) {
	d.c.ToCnf(g)
}

func (d *dict) AssumeConstraints(s inter.S) {
	for m := range d.constraints {
		s.Assume(m)
	}
}

// CardinalityConstrainer constructs a sorting network to provide
// cardinality constraints over the provided slice of literals. Any
// new clauses and variables are translated to CNF and taught to the
// given inter.Adder, so this function will panic if it is in a test
// context.
func (d *dict) CardinalityConstrainer(g inter.Adder, ms []z.Lit) *logic.CardSort {
	clen := d.c.Len()
	cs := d.c.CardSort(ms)
	marks := make([]int8, clen, d.c.Len())
	for i := range marks {
		marks[i] = 1
	}
	for w := 0; w <= cs.N(); w++ {
		marks, _ = d.c.CnfSince(g, marks, cs.Leq(w))
	}
	return cs
}

// MandatoryIdentifiers returns a slice containing the Identifiers of
// every Installable with at least one "Mandatory" constraint, in the
// order they appear in the input.
func (d *dict) MandatoryIdentifiers() []Identifier {
	var ids []Identifier
	for _, installable := range d.inorder {
		for _, constraint := range installable.Constraints() {
			if _, ok := constraint.(mandatory); ok {
				ids = append(ids, installable.Identifier())
				break
			}
		}
	}
	return ids
}

func (d *dict) Installables(g *gini.Gini) []Installable {
	var result []Installable
	for _, i := range d.inorder {
		if g.Value(d.LitOf(i.Identifier())) {
			result = append(result, i)
		}
	}
	return result
}

func (d *dict) Lits(dst []z.Lit) []z.Lit {
	if cap(dst) < len(d.inorder) {
		dst = make([]z.Lit, 0, len(d.inorder))
	}
	dst = dst[:0]
	for _, i := range d.inorder {
		m := d.LitOf(i.Identifier())
		dst = append(dst, m)
	}
	return dst
}

func (d *dict) Conflicts(g *gini.Gini) []AppliedConstraint {
	whys := g.Why(nil)
	as := make([]AppliedConstraint, 0, len(whys))
	for _, why := range whys {
		if a, ok := d.constraints[why]; ok {
			as = append(as, a)
		}
	}
	return as
}
