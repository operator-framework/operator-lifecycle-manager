// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package gini

import (
	"io"
	"time"

	"github.com/irifrance/gini/dimacs"
	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/internal/xo"
	"github.com/irifrance/gini/z"
)

// Gini is a concrete implementation of solver
type Gini struct {
	xo *xo.S
}

// internal use
func newGiniXo(x *xo.S) *Gini {
	g := &Gini{
		xo: x}
	return g
}

// New creates a new gini solver.
func New() *Gini {
	g := &Gini{
		xo: xo.NewS()}
	return g
}

// NewDimacs create a new gini solver from
// dimacs formatted input.
func NewDimacs(r io.Reader) (*Gini, error) {
	vis := &xo.DimacsVis{}
	if e := dimacs.ReadCnf(r, vis); e != nil {
		return nil, e
	}
	g := &Gini{
		xo: vis.S()}
	return g, nil
}

// NewV creates a new Gini solver with
// hint for capacity of variables set to capHint.
func NewV(capHint int) *Gini {
	g := &Gini{
		xo: xo.NewSV(capHint)}
	return g
}

// NewVc creates a new Gini solver with
// hint for capacity of variables set to vCapHint,
// and likewise capacity of clauses set cCapHint
func NewVc(vCapHint, cCapHint int) *Gini {
	g := &Gini{
		xo: xo.NewSVc(vCapHint, cCapHint)}

	return g
}

// Copy makes a copy of the Gini g.
//
// every bit of g is copied except
//
//  1. Statistics for reporting, which are set to 0 instead of copied
//  2. Control mechanisms for Solve's resulting from GoSolve() so the
//     copied gini can make its own calls to GoSolve() (or Solve()) without
//     affecting the original.
func (g *Gini) Copy() *Gini {
	other := &Gini{
		xo: g.xo.Copy()}
	return other
}

// SCopy implements inter.S
func (g *Gini) SCopy() inter.S {
	return g.Copy()
}

// MaxVar returns the variable whose id is max.
func (g *Gini) MaxVar() z.Var {
	return g.xo.Vars.Max
}

// Lit produces a fresh variable and returns its positive literal, conforming
// to inter.Liter.
func (g *Gini) Lit() z.Lit {
	return g.xo.Lit()
}

// Add implements inter.S.  To add a clause (x + y + z),
// one calls
//
//  g.Add(x)
//  g.Add(y)
//  g.Add(z)
//  g.Add(0)
//
// If Add is called under a test scope, then Add will panic
// if `m` is 0/LitNull.
//
func (g *Gini) Add(m z.Lit) {
	g.xo.Add(m)
}

// Assume causes the solver to make the assumption
// that m is true in the next call to Solve() or the
// next call to Test().
//
// Solve() always consumes and forgets untested assumptions.
// tested assumptions are never forgotten, and may be popped
// with Untest().
func (g *Gini) Assume(ms ...z.Lit) {
	g.xo.Assume(ms...)
}

// Solve solves the constraints.  It returns 1 if
// sat, -1 if unsat, and 0 if canceled.
func (g *Gini) Solve() int {
	res := g.xo.Solve()
	return res
}

// Try solves with a timeout.  Try returns
//  1  if sat
//  -1 if unsat
//  0  if timeout
func (g *Gini) Try(dur time.Duration) int {
	return g.xo.Try(dur)
}

// GoSolve provides a connection to a single background
// solving goroutine, a goroutine which calls Solve()
func (g *Gini) GoSolve() inter.Solve {
	return g.xo.GoSolve()
}

// Value returns the truth value of the literal m.
// The meaning of the returned value is only defined
// if the previous call to sat was satisfiable.  In
// this case, the returned value is the value of m
// in a model of of the underlying problem, where that
// model is determined by the previous call to Solve().
func (g *Gini) Value(m z.Lit) bool {
	return g.xo.Vars.Vals[m] == 1
}

// Why returns the slice of failed assumptions, a minimized
// set of assumptions which are sufficient for the last
// UNSAT result (from a call to Test() or Solve()).
//
// Why tries to store the failed assumptions in ms, if
// there is sufficient space.
func (g *Gini) Why(ms []z.Lit) []z.Lit {
	return g.xo.Why(ms)
}

// Test tests whether the current assumptions are consistent under BCP
// and opens a scope for future assumptions.
//
// Test returns the result of BCP res
//  (1: SAT, -1: UNSAT: 0, UNKNOWN)
// And any associated data in out.  The data tries to use dst
// for storage if there is space.
//
// The associated data is
//
//  - All assigned literals since last test if SAT or UNKNOWN
//  - Either the literals of a clause which is unsat under BCP or an assumption
//    which is false under BCP, whichever is found first.
//
// When g is under a test scope, many operations are not
// possible, in particular: {Add,Activate,ActivateWith,Deactivate}
// are unsupported operations under a test scope.
func (g *Gini) Test(dst []z.Lit) (res int, out []z.Lit) {
	return g.xo.Test(dst)
}

// Untest removes a scope opened and sealed by Test, backtracking
// and removing assumptions.
//
// Untest returns whether the problem is consistent under BCP after
// removing assumptions from the last Test.  It can happen that
// a given set of assumptions becomes inconsistent under BCP
// as the underlying solver learns clauses.
func (g *Gini) Untest() int {
	return g.xo.Untest()
}

// Reasons give a set of literals which imply m by virtue of
// a single clause.
//
// Reasons only works if m was returned by some call to Test resulting in
// SAT or UNKNOWN.  Otherwise, Reasons is undefined and may panic.
//
// Additionally, Reasons returns a piece of an acyclic implication graph.
// The entire graph may be reconstructed by calling Reasons for every propagated
// literal returned by Test.  The underlying graph changes on calls to Test
// and Solve.  If the underlying graph does not change, then Reasons guarantees
// that it is acyclic.
func (g *Gini) Reasons(dst []z.Lit, m z.Lit) []z.Lit {
	return g.xo.Reasons(dst, m)
}

// Create a clause from the last (non 0-terminated, non-empty) sequence of Adds and
// `m.Not()`.  Activate panics if the last sequence of Adds since Add(0) is empty,
// in other words, don't try to use an activation literal for the empty clause.
//
// Additionally, in this case subsequent behavior of `g` is undefined.  The caller
// should verify a clause is not empty using `g.Value(m)` for all literals in in
// the clause to activate.
//
// To active the clause, assume `m`.
//
// Example:
//
//  if g.Value(a) != false || g.Value(b) != false {
//    g.Add(a)
//    g.Add(b)
//    m := g.Activate()  // don't call g.Add(0).
//    g.Assume(m) // now the clause (a + b)  is active
//    if g.Solve() == 1 {
//       // do something
//    }
//    // now the clause (a+b) is not active.
//    g.Deactivate(m)
//    // now `m` can be re-used and the solver can ignore
//    // clauses with `m`.
//  }
//
// Activation of all clauses can be used in conjunction with cardinality constraints
// to easily create a MAXSAT solver.
//
// Activate is an unsupported operation under a test scope
// and will panic if called under a test scope.
func (g *Gini) Activate() (m z.Lit) {
	return g.xo.Activate()
}

// ActivateWith allows the caller to specify the activation literal.  It is
// useful when the caller needs to activate many clauses with one literal.
// However, please note that activation literals are volatile and will be
// recycled on Deactivate.  As with Activate, ActivateWith should not
// be used to activate the empty clause.  In this case, ActivateWith
// panics and subsequent behavior of g is undefined.
//
// ActivateWith is an unsupported operation under a test scope
// and will panic if called under a test scope.
func (g *Gini) ActivateWith(act z.Lit) {
	g.xo.ActivateWith(act)
}

// ActivationLit returns a new literal to be used with ActivateWith().
//
// ActivationLit is an unsupported operation under a test scope and will
// panic if called under a test scope.
func (g *Gini) ActivationLit() z.Lit {
	return g.xo.ActivationLit()
}

// Deactivate deactivates a literal as returned by Activate.  Deactivation
// frees the literal for future Activations and removes all clauses, including
// learned clauses, which contain `m.Not()`.
//
// Deactivate is an unsupported operation under a test scope
// and will panic if called under a test scope.
func (g *Gini) Deactivate(m z.Lit) {
	g.xo.Deactivate(m)
}

// Write writes the underlying CNF in dimacs format to dst,
// returning any i/o error which occured in the process.
func (g *Gini) Write(dst io.Writer) error {
	return g.xo.Cdb.Write(dst)
}
