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

	// teach all constraints to the solver
	s.litMap.AddConstraints(s.g)

	// collect literals of all mandatory installables to assume as a baseline
	var assumptions []z.Lit
	for _, anchor := range s.litMap.MandatoryIdentifiers() {
		assumptions = append(assumptions, s.litMap.LitOf(anchor))
	}

	// assume that all constraints hold
	s.litMap.AssumeConstraints(s.g)
	s.g.Assume(assumptions...)

	var aset map[z.Lit]struct{}
	// push a new test scope with the baseline assumptions, to prevent them from being cleared during search
	outcome, _ := s.g.Test(nil)
	if outcome != satisfiable && outcome != unsatisfiable {
		// searcher for solutions in input order, so that preferences
		// can be taken into acount (i.e. prefer one catalog to another)
		outcome, assumptions, aset = (&search{s: s.g, lits: s.litMap, tracer: s.tracer}).Do(context.Background(), assumptions)
	}
	switch outcome {
	case satisfiable:
		s.buffer = s.litMap.Lits(s.buffer)
		var extras, excluded []z.Lit
		for _, m := range s.buffer {
			if _, ok := aset[m]; ok {
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
		s.g.Assume(assumptions...)
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
		var err error
		s.litMap, err = newLitMapping(input)
		return err
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
			var err error
			s.litMap, err = newLitMapping(nil)
			return err
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
