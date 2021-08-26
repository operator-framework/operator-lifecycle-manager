// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

// Package z provides minimal common interface to lits and vars.
//
// Variables and literals are represented as uint32s.  The LSB
// indicates for a literal whether or not it is the negation of a
// variable.
//
// As is common in SAT solvers, this representation is convenient for
// data structures indexed by variable or literal.
package z
