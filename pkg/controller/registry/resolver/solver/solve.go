package solver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/irifrance/gini"
	"github.com/irifrance/gini/z"
)

// Identifier values uniquely identify particular Installables within
// the input to a single call to Solve.
type Identifier string

func (id Identifier) String() string {
	return string(id)
}

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

type Solver interface {
	Solve(context.Context) ([]Installable, error)
}

type solver struct {
	g      *gini.Gini
	dict   *dict
	tracer Tracer
	buffer []z.Lit
}

const (
	satisfiable   = 1
	unsatisfiable = -1
)

// Solve takes a slice containing all Installables and returns a slice
// containing only those Installables that were selected for
// installation. If no solution is possible, or if the provided
// Context times out or is cancelled, an error is returned.
func (s *solver) Solve(ctx context.Context) (result []Installable, err error) {
	defer func() {
		// This likely indicates a bug, so discard whatever
		// return values were produced.
		if derr := s.dict.Error(); derr != nil {
			result = nil
			err = derr
		}
	}()

	s.dict.AddConstraints(s.g)
	h := searcher{
		solver:  s,
		assumed: &orderedLitSet{indices: make(map[z.Lit]int)},
	}
	for _, anchor := range s.dict.MandatoryIdentifiers() {
		h.assumed.Add(s.dict.LitOf(anchor))
	}
	s.g.Assume(h.assumed.Slice()...)
	s.dict.AssumeConstraints(s.g)

	outcome, _ := s.g.Test(nil)
	if outcome != satisfiable && outcome != unsatisfiable {
		outcome = h.search(ctx, 0)
	}
	switch outcome {
	case satisfiable:
		s.buffer = s.dict.Lits(s.buffer)
		var extras, excluded []z.Lit
		for _, m := range s.buffer {
			if h.assumed.Contains(m) {
				continue
			}
			if !s.g.Value(m) {
				excluded = append(excluded, m.Not())
				continue
			}
			extras = append(extras, m)
		}
		s.g.Untest()
		cs := s.dict.CardinalityConstrainer(s.g, extras)
		s.g.Assume(h.assumed.Slice()...)
		s.g.Assume(excluded...)
		s.dict.AssumeConstraints(s.g)
		_, s.buffer = s.g.Test(s.buffer)
		for w := 0; w <= cs.N(); w++ {
			s.g.Assume(cs.Leq(w))
			if s.g.Solve() == satisfiable {
				return s.dict.Installables(s.g), nil
			}
		}
		// Something is wrong if we can't find a model anymore
		// after optimizing for cardinality.
		return nil, fmt.Errorf("unexpected internal error")
	case unsatisfiable:
		return nil, NotSatisfiable(s.dict.Conflicts(s.g))
	}

	return nil, Incomplete
}

type orderedLitSet struct {
	indices map[z.Lit]int
	lits    []z.Lit
}

func (set *orderedLitSet) Add(m z.Lit) {
	if set.Contains(m) {
		return
	}
	set.indices[m] = len(set.lits)
	set.lits = append(set.lits, m)
}

func (set *orderedLitSet) Remove(m z.Lit) {
	if index, ok := set.indices[m]; ok {
		set.lits = append(set.lits[:index], set.lits[index+1:]...)
		delete(set.indices, m)
	}
}

func (set *orderedLitSet) Contains(m z.Lit) bool {
	_, ok := set.indices[m]
	return ok
}

func (set *orderedLitSet) Slice() []z.Lit {
	return set.lits
}

func (set *orderedLitSet) Len() int {
	return len(set.lits)
}

type searcher struct {
	solver  *solver
	assumed *orderedLitSet
}

// search traverses installables, in the order they appear in the
// input, looking for a set of assumptions to satisfy each dependency
// constraint. Identifiers appearing earlier within a particular
// constraint are preferred. Like (*gini.Gini).Solve(), search always
// returns either satisfiable or unsatisfiable.
func (h *searcher) search(ctx context.Context, upto int) int {
	if upto >= h.assumed.Len() {
		return h.solver.g.Solve()
	}

	for _, constraint := range h.solver.dict.InstallableOf(h.assumed.Slice()[upto]).Constraints() {
		dependencies := constraint.order()
		skip := len(dependencies) == 0
		var ms []z.Lit
		for _, dependency := range dependencies {
			m := h.solver.dict.LitOf(dependency)
			if h.assumed.Contains(m) {
				skip = true
				break
			}
			ms = append(ms, h.solver.dict.LitOf(dependency))
		}
		if skip {
			// Constraint already satisfied!
			continue
		}
		for _, m := range ms {
			h.assumed.Add(m)
			h.solver.g.Assume(m)

			var outcome int
			outcome, h.solver.buffer = h.solver.g.Test(h.solver.buffer)
			if outcome != satisfiable && outcome != unsatisfiable {
				outcome = h.search(ctx, upto+1)
			}
			switch outcome {
			case satisfiable:
				h.solver.g.Untest()
				return satisfiable
			case unsatisfiable:
				h.solver.tracer.Trace(h)
				h.assumed.Remove(m)
				if h.solver.g.Untest() == unsatisfiable {
					return unsatisfiable
				}
			default:
				panic("search returned unexpected value")
			}
		}
	}

	return h.search(ctx, upto+1)
}

func (h *searcher) Installables() []Installable {
	ms := h.assumed.Slice()
	if len(ms) == 0 {
		return nil
	}
	result := make([]Installable, len(ms))
	for i := 0; i < len(result); i++ {
		result[i] = h.solver.dict.InstallableOf(ms[i])
	}
	return result
}

func (h *searcher) Conflicts() []AppliedConstraint {
	return h.solver.dict.Conflicts(h.solver.g)
}

func New(options ...Option) (Solver, error) {
	s := solver{g: gini.New()}
	for _, option := range append(options, defaults...) {
		if err := option(&s); err != nil {
			return nil, err
		}
	}
	return &s, nil
}

type Option func(s *solver) error

func WithInput(input []Installable) Option {
	return func(s *solver) error {
		s.dict = compileDict(input)
		return nil
	}
}

func WithTracer(t Tracer) Option {
	return func(s *solver) error {
		s.tracer = t
		return nil
	}
}

var defaults = []Option{
	func(s *solver) error {
		if s.dict == nil {
			s.dict = compileDict(nil)
		}
		return nil
	},
	func(s *solver) error {
		if s.tracer == nil {
			s.tracer = DefaultTracer{}
		}
		return nil
	},
}
