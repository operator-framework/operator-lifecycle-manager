package solver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/irifrance/gini"
	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/z"
)

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
	g      inter.S
	litMap *litMapping
	tracer Tracer
	buffer []z.Lit
}

const (
	satisfiable   = 1
	unsatisfiable = -1
	unknown       = 0
)

// Solve takes a slice containing all Installables and returns a slice
// containing only those Installables that were selected for
// installation. If no solution is possible, or if the provided
// Context times out or is cancelled, an error is returned.
func (s *solver) Solve(ctx context.Context) (result []Installable, err error) {
	defer func() {
		// This likely indicates a bug, so discard whatever
		// return values were produced.
		if derr := s.litMap.Error(); derr != nil {
			result = nil
			err = derr
		}
	}()

	// add constraints from the initial set of installables
	s.litMap.AddConstraints(s.g)

	// the ordered searcher can look for solutions in input order, so that preferences
	// can be taken into acount (i.e. prefer one catalog to another)
	orderedSearcher := searcher{
		solver:  s,
		assumed: &orderedLitSet{indices: make(map[z.Lit]int)},
	}

	// mandatory literals should come first in the ordered search
	for _, anchor := range s.litMap.MandatoryIdentifiers() {
		orderedSearcher.assumed.Add(s.litMap.LitOf(anchor))
	}

	// the base solver should also assume mandatory literals
	s.g.Assume(orderedSearcher.assumed.Slice()...)

	// the litMapping needs to be updated with the new constraints from adding mandatory items
	s.litMap.AssumeConstraints(s.g)

	// check if we have enough information to return sat/unsat after just the mandatory items
	outcome, _ := s.g.Test(nil)
	if outcome != satisfiable && outcome != unsatisfiable {
		// use the searcher to walk through installables and see if we can determin sat/unsat
		outcome = orderedSearcher.search(ctx)
	}
	switch outcome {
	case satisfiable:
		s.buffer = s.litMap.Lits(s.buffer)
		var extras, excluded []z.Lit
		for _, m := range s.buffer {
			if orderedSearcher.assumed.Contains(m) {
				continue
			}
			if !s.g.Value(m) {
				excluded = append(excluded, m.Not())
				continue
			}
			extras = append(extras, m)
		}
		s.g.Untest()
		cs := s.litMap.CardinalityConstrainer(s.g, extras)
		s.g.Assume(orderedSearcher.assumed.Slice()...)
		s.g.Assume(excluded...)
		s.litMap.AssumeConstraints(s.g)
		_, s.buffer = s.g.Test(s.buffer)
		for w := 0; w <= cs.N(); w++ {
			s.g.Assume(cs.Leq(w))
			if s.g.Solve() == satisfiable {
				return s.litMap.Installables(s.g), nil
			}
		}
		// Something is wrong if we can't find a model anymore
		// after optimizing for cardinality.
		return nil, fmt.Errorf("unexpected internal error")
	case unsatisfiable:
		return nil, NotSatisfiable(s.litMap.Conflicts(s.g))
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

type depthTrackingS interface {
	inter.S
	Unwind()
}

type depthTrackingGini struct {
	inter.S
	depth int
}

var _ depthTrackingS = &depthTrackingGini{}

func newDepthTrackingGini(s inter.S) *depthTrackingGini {
	return &depthTrackingGini{
		S:     s,
		depth: 0,
	}
}

func (g *depthTrackingGini) Test(dst []z.Lit) (result int, out []z.Lit) {
	result, out = g.S.Test(dst)
	g.depth++
	return
}

func (g *depthTrackingGini) Untest() int {
	outcome := g.S.Untest()
	g.depth--
	return outcome
}

func (g *depthTrackingGini) Unwind() {
	for i := 0; i < g.depth; i++ {
		g.Untest()
	}
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
func (h *searcher) search(ctx context.Context) int {
	unwindableSolver := newDepthTrackingGini(h.solver.g)
	defer unwindableSolver.Unwind()

	canSatisfyConstraints := func(m z.Lit) (outcome int) {
		for _, constraint := range h.solver.litMap.InstallableOf(m).Constraints() {
			dependencies := constraint.order()
			skip := len(dependencies) == 0
			var ms []z.Lit
			for _, dependency := range dependencies {
				m := h.solver.litMap.LitOf(dependency)
				if h.assumed.Contains(m) {
					skip = true
					break
				}
				ms = append(ms, h.solver.litMap.LitOf(dependency))
			}
			if skip {
				// Constraint already satisfied!
				continue
			}
			for _, m := range ms {
				h.assumed.Add(m)
				unwindableSolver.Assume(m)

				outcome, h.solver.buffer = unwindableSolver.Test(h.solver.buffer)
				switch outcome {
				case satisfiable:
					unwindableSolver.Untest()
					return
				case unsatisfiable:
					h.solver.tracer.Trace(h)
					h.assumed.Remove(m)
					if unwindableSolver.Untest() == unsatisfiable {
						return
					}
				default:
					return
				}
			}
		}
		return
	}

	for i := 0; i < h.assumed.Len(); i++ {
		outcome := canSatisfyConstraints(h.assumed.Slice()[i])
		if outcome != unknown {
			return outcome
		}
	}

	return unwindableSolver.Solve()
}

func (h *searcher) Installables() []Installable {
	ms := h.assumed.Slice()
	if len(ms) == 0 {
		return nil
	}
	result := make([]Installable, len(ms))
	for i := 0; i < len(result); i++ {
		result[i] = h.solver.litMap.InstallableOf(ms[i])
	}
	return result
}

func (h *searcher) Conflicts() []AppliedConstraint {
	return h.solver.litMap.Conflicts(h.solver.g)
}

func New(options ...Option) (Solver, error) {
	s := solver{g: newDepthTrackingGini(gini.New())}
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
		s.litMap = newLitMapping(input)
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
		if s.litMap == nil {
			s.litMap = newLitMapping(nil)
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
