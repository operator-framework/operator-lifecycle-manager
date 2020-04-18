// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package z

import "fmt"

type Lit uint32

// LitNull is the constant 0, used in various places to indicate
// a meaningless literal.
const LitNull Lit = 0

// Dimacs2Lit takes a dimacs-coded literal and returns a Lit.
// A Dimacs coded literal for a variable v is
//
//  -v for not(v)
//   v for v
func Dimacs2Lit(m int) Lit {
	if m < 0 {
		return Lit(-2*m + 1)
	}
	return Lit(2 * m)
}

// Dimacs returns the dimacs coding of the Lit m.
func (m Lit) Dimacs() int {
	if m&1 != 0 {
		return -int((m >> 1))
	}
	return int(m >> 1)
}

func (m Lit) String() string {
	return fmt.Sprintf("%d", m.Dimacs())
}

// Var returns the Var associated with m.
func (m Lit) Var() Var {
	return Var(m >> 1)
}

// Not returns the negation of m.
func (m Lit) Not() Lit {
	return Lit(m ^ 1)
}

// Sign returns
//
//  1  if m is a variable
// -1 if m is a negated variable
func (m Lit) Sign() int8 {
	if m&1 == 0 {
		return 1
	}
	return -1
}

// IsPos returns true if m is a variable.
func (m Lit) IsPos() bool {
	return m&1 == 0
}
