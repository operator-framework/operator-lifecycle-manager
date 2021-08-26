// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

// Package xo is the main gini internal package for (single core) sat solving.
//
// Sincerest apologies to newcomers, as successfully editing this package for
// all but simple incidental changes likely requires a fairly deep
// understanding of CDCL sat solvers; this fact is probablly largely
// unavoidable.
//
// For an introduction to SAT, see The Handbook of satisfiability.
//
// That said, this solver is organised as follows.
//
// Variables and Literals
//
// Variables (type Var) are uint32, starting at offset 1 ending at 2^30 - 1.
// Each variable can either have value true or false, represented by sign
// {+1,-1}, or by an associated literal.
//
// Literals (type Lit) are uint32, starting at 2, ending at 2^31 - 1.
// Literals are either variables or their negation.
//
//
// Clauses
//
//   Clauses are constraints in the form of disjunctions of literals.
//
// Clauses have a complicated representation for efficiency reasons clauses
// have an underlying data store, (type CDat in cdat.go),  which is one big
// slice of 32bit values, most of which are Lits.   A compact header (32bit,
// type Chd) contains some metadata for each clause.
//
//  - whether the clause is learnt
//  - the literal block distance of the
//     clause (number of non deterministic branches it spanned when created, if
//     learnt)
//  - an abstraction of the size of the clause
//  - "heat", a heuristic score of activity in solving.
//
// Each clause is also associated with a z.C, which is a 32bit offset into
// the data store slice of lits.  This is the identifier for a clause, which
// can change over time due to clause garbage collection during the solving
// process.
//
// Each z.C p points to data layed out as follows.
//
//   [... Chd lit0 lit1 ... litn LitNull]
//            p
//
// Solving
//
// Solving is a depth first search over the (exponential) space of variable
// assignments.  As the search takes place the value of the clauses in the
// assignment stack may imply other values.  For example, if we search for an
// assignment for variables {x,y}, and we guess x=1, and there is a clause
// (not(x) or not(y)), then not(y) must be part of any extension of the
// assignment.  Identifying such necessary assignment extensions is called
// (Boolean) constraint propagation / BCP
//
// During BPC, it is possible to find a clause in which every literal is
// false.  Such a clause is called a "conflict".  Also the event of
// identifying such a clause is called a conflict..
//
// Conflicts are resolved by backtracking and choosing a new branch.
// Additionally, a new clause is derived which prevents the search from
// arriving at the same partial assignment, irrespective of the order in which
// the search applies (at least as long as that clause is kept in the solving
// process...).  Such a clause is called a "learnt" clause and the process of
// adding such clauses is called "learning".
//
// As conflicts are encountered and new clauses are learned, it becomes
// necessary to remove learned clauses in such a way as to guarantee progress
// of the search algorithm.  This is because lots of clauses can have a high
// ratio of irrelevant clauses to the future search over time, and the more
// clauses managed by the solver, the more expensive BCP becomes.
//
// Since BCP is the bottlneck in the algorithm, some balance must be achieved
// by removing learned clauses.  This is where "compaction" or "clause garbage
// collection" comes into play.  This is implemented in cgc.go and is based on
// some heuristics from the literature.
//
// This depth first search has the following major components, in term of code
// organisation.
//
//  - Guess (type Guess, in guess.go) guessing variables and associated values
//  during the search (for example, guessing x=1 in the search above).
//
//  - Trail manages the assignment stack and propagation of constraints.
//
//  - Derive learns new clauses whenever the search reaches a point which cannot be
//  extended because some clause is false.  The new clauses prune the future
//  search space more than just backtracking.
//
// Constraint propagation
//
// Constraint propagation is the most optimised and difficult to manage part
// of the code.  It uses "watch lists", which associates 2 literals with every
// clause (except 1 literal clauses).  Whenever one of those literals is
// falsified in the search, the clause is examined to see whether or not it
// implies a new value for a variable or if it is false.
//
// Important Heuristics
//
// Variable ordering in Guess is extremely important to solving time.
// Variables are re-ordered dynamically according to an approximation of their
// proximity to/involvement with conflicts
//
// Clause Garbage Collection is also extremely important.  We use a
// hyper-aggressive strategy which multiplexes the CGC frequency using the
// luby series and removes approximately half of the learnted clauses in each
// CGC.
//
// Variable naming
//
// Gini uses regular succint variable naming where possible to aid in
// readability and succintness
//
//  - Lit; m,n,o
//  - Var: u,v
//  - z.C: p,q,r
//  - Watch: w -
//  - Slices ([]): add an s on the end of underlying type
//  - Slice indices: i,j,k
//  - Chd: h
package xo
