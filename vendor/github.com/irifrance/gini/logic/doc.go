// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

// Package logic provides representation of Boolean combinational and sequential
// logic.
//
// Package logic uses a standard
// https://en.wikipedia.org/wiki/And-inverter_graph (and-inverter graph) to
// represent circuits.  They are simplified using some rules and structural
// hashing, implemented in the type C.  Additionally, Cardinality constraints
// are supported.
//
// Unlike most AIG libraries, package logic uses the same variables and literals
// as an associated SAT solver.  This means that there is no need to maintain
// maps for AIG<->SAT flows.
//
// Package logic also supports simple sequential logic (with latches and
// unrolling) in the type S.
package logic
