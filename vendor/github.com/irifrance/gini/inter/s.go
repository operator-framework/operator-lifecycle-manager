// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package inter

import (
	"time"

	"github.com/irifrance/gini/z"
)

// Interface Solveable encapsulates a decision
// procedure which may run for a long time.
//
// Solve/Try returns
//
//  1  If the problem is SAT
//  0  If the problem is undetermined (Try only)
//  -1 If the problem is UNSAT
//
// These error codes are used throughout gini.
type Solvable interface {
	Solve() int
	Try(dur time.Duration) int
}

// Interface GoSolvable encapsulates a handle
// on a Solve running in its own goroutine.
type GoSolvable interface {
	GoSolve() Solve
}

// Adder encapsulates something to which
// clauses can be added by sequences of
// z.LitNull-terminated literals.
type Adder interface {

	// add a literal to the clauses.  if m is z.LitNull,
	// signals end of clause.
	//
	// If the implemation of Add is a solver under a test scope
	// then Add undoes the test.
	//
	Add(m z.Lit)
}

// Interface MaxVar is something which records the
// maximum variable from a stream of inputs (such
// as Adds/Assumes) and can return the maximum of
// all such variables.
type MaxVar interface {
	MaxVar() z.Var
}

// Liter produces fresh variables and returns the corresponding
// positive literal.
//
type Liter interface {
	Lit() z.Lit
}

// Model encapsulates something from which a model
// can be exracted.
type Model interface {
	Value(m z.Lit) bool
}

// Assumable encapsulates a problem
type Assumable interface {
	Assume(m ...z.Lit)
	Why(dst []z.Lit) []z.Lit
}

// Interface Testable provides an interface for scoped
// assumptions.
//
// Testable provides scoped light weight Solving and tracking
// of implications.
//
// A Solvable and Assumable which also implements Testable has the following
// semantics w.r.t. Assumptions.  All calls to Assume which are subsequently
// Tested before calling Solve() remain until a corresponding call to Untest.
//
// Put another way, Solve() consumes and forgets any untested assumptions
// for a given Solve process.  To forget tested assumptions, the user
// must call Untest().  Tests and Untests may be nested.
type Testable interface {
	Assumable

	// Test the current assumptions under unit propagation.
	// append the resulting propagated literals since the last
	// test in dst, if dst is not nil, and return
	//
	//  result: -1 for UNSAT, 1 for SAT, 0 for UNKNOWN
	//  out: the propagated literals since last Test,
	//       stored in dst if possible, or nil if out is nil.
	//
	// Once Test is called on a set of assumptions, all
	// future calls to Solve do not consume and forget
	// assumptions prior to test.
	Test(dst []z.Lit) (result int, out []z.Lit)

	// Untest removes the assumptions from the last test.
	// Untest returns -1 if the result is UNSAT, and
	// 0 (indicating unknown) otherwise.  If the result is
	// -1, then Test should not be called.
	Untest() int

	// Reasons returns the reasons for implied, storing
	// the result in rs if possible.
	Reasons(rs []z.Lit, implied z.Lit) []z.Lit
}

// Interface S encapsulates something capable
// of a complete incremental SAT interface
// enabling composing solvable, assumable, model, testable,
// and GoSolveable.
type S interface {
	MaxVar
	// Although an S can generate literals via Liter, it
	// doesn't have to.  One can just send arbitrary variables
	// via Adder, Assume, etc.  Liter is useful for applications
	// which need a way to know how to generate new variables/literals.
	Liter
	Adder
	Solvable
	GoSolvable
	Model
	Testable

	// Can create a copy.  A copy copies everything in the S interface
	// and nothing more (such as simplifiers).
	SCopy() S
}

// Interface Sv encapsulates an S which has the need or capacity to use inner
// variables which are hidden from the user.
type Sv interface {
	S

	// Inner returns the positive literal of a new inner variable.
	Inner() z.Lit

	// FreeInner frees the previously inner-allocated variable
	// associated with m.  If m's variables was not previously
	// allocated with Inner, then FreeInner and all subsequent
	// usage of Sv is undefined.
	FreeInner(m z.Lit)
}

// Interface Sc is an interface for a concurrent solver which
// must be stopped in order to free goroutines.
type Sc interface {
	S

	// Stop stops the Sc and should be called once.  Once stop
	// is called all behavior of Sc is undefined.
	Stop()
}

// Activatable provides support for recyclable activation literals
//
// Caveats: activation clauses must not be empty under unit propagtion
// at level 0.  The caller should ensure this by construction.
type Activatable interface {
	// Activate should be called in place of Add(0) to activate a
	// clause.  Activate returns the activation literal, which, if assigned
	// activates the last added clause.
	//
	// If the last clause is empty, then Activate panics.  The caller
	// should only activate non-empty clauses.  Note that in incremental
	// settings, one may have to verify whether or not a clause is
	// empty.
	//
	// like `Add()`, Activate should only be called at decision level 0.
	Activate() z.Lit

	// ActivateWith is like Activate but it allows the caller to specify
	// the activation literal `act`.  The activation literal should
	// be `pure`, meaning that `act.Not()` does not appear anywhere in
	// any clause added.  Note that deactivation of literals passed to
	// ActivateWith causes them to be recycled.
	ActivateWith(act z.Lit)

	// ActivationLit returns a literal to be used with ActivateWith.
	// As all other Activation related methods, ActivationLit is
	// not supported under test scopes.
	ActivationLit() z.Lit

	// Deactivate deactivates an activation literal as returned by
	// Activate.
	//
	// like `Add()`, Deactivate should only be called at decision level 0.
	Deactivate(m z.Lit)
}
