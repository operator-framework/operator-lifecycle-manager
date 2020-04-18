// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package dimacs

import "github.com/irifrance/gini/z"

// Type Vis provides a visitor interface to reading dimacs files.
//
// Anything implementing Vis can read a dimacs file.
type CnfVis interface {

	// Init is called on a problem line defining number of variables and
	// number of clauses.  If this is not given and strict enforcement
	// of their presence is lacking, then this is called with some defaults.
	Init(v, c int)

	// Add adds a dimacs literal as an int
	Add(m z.Lit)

	// Called at end of file.
	Eof()
}

// Interface ICnfVis is an interface for eading icnf files.
type ICnfVis interface {

	// Add adds a literal like inter.Adder.
	Add(m z.Lit)

	// Assume is called in a sequence and 0-terminated.
	// 0-termination will normally trigger a solve
	Assume(m z.Lit)

	// EOF
	Eof()
}

// Interface SolveVis is a visitor for reading solver outputs.
type SolveVis interface {

	// Solution is called for a solution line of a solver output.
	//
	// As usual
	//  1 is sat, -1 is unsat, 0 is unknown
	Solution(r int)

	// Value gives a value
	Value(m z.Lit)

	// End of output
	Eof()
}
