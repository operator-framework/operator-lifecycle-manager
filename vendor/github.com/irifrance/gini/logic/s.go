// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package logic

import "github.com/irifrance/gini/z"

// S adds sequential elements to C, gini's combinational
// logic representation.
type S struct {
	C               // the combinational circuit, logic.C
	Latches []z.Lit // the memory elements/state variables
}

// NewS creates a new sequential circuit, with latches.
func NewS() *S {
	s := &S{Latches: make([]z.Lit, 0, 128)}
	initC(&s.C, 256)
	return s
}

// NewSCap creates a new sequential circuit with initial capacity capHint.
func NewSCap(capHint int) *S {
	s := &S{Latches: make([]z.Lit, 0, capHint)}
	initC(&s.C, capHint)
	return s
}

// Copy make a copy of s.
func (s *S) Copy() *S {
	cc := (&s.C).Copy()
	ls := make([]z.Lit, len(s.Latches))
	copy(ls, s.Latches)
	return &S{C: *cc, Latches: ls}
}

// Latch returns a new "latch", which is a value that
// evolves over time.
//
// The definition of the value of a latch is in discrete
// time, in which at time 0, the value of the latch is init
// The value of a latch at time i is the value of the next
// state literal of the latch at time i-1.  By default, this
// literal is the latch itself.  It may be set using SetNext
// below.
//
// init must be one of the following
//
//  s.T
//  s.F
//  z.LitNull
//
// or Latch will panic.  z.LitNull means uninitialized, or unknown
// or 'X' in ternary logic.
//
func (s *S) Latch(init z.Lit) z.Lit {
	if init != s.F && init != s.T && init != z.LitNull {
		panic("invalid initial value")
	}
	n, i := s.newNode()
	n.a = init
	n.b = z.Var(i).Pos()
	s.Latches = append(s.Latches, n.b)
	return n.b
}

// Next returns the next state literal for m.
func (s *S) Next(m z.Lit) z.Lit {
	n := &s.nodes[m.Var()]
	if n.a != s.F && n.a != s.T && n.a != z.LitNull {
		panic("m not a latch")
	}
	return n.b
}

// SetNext sets the next state literal for m to nxt.
// m should be returned from s.Latch() or SetNext will
// panic.  nxt should be a literal returned by P.Latch,
// s.In or s.And or the subsequent behavior of p is undefined.
func (s *S) SetNext(m, nxt z.Lit) {
	n := &s.nodes[m.Var()]
	if n.a != s.F && n.a != s.T && n.a != z.LitNull {
		panic("m not a latch")
	}
	n.b = nxt
}

// Init returns the initial state of the latch latch.
//
//  - s.F if false
//  - s.T if true
//  - z.LitNull if X
func (s *S) Init(latch z.Lit) z.Lit {
	v := latch.Var()
	return s.nodes[v].a
}

// SetInit sets the initial state of 'latch' to 'nxt'.  'nxt' should be either
// z.LitNull, s.T, or s.F.  If it is not, SetInit panics.  If 'latch' is not
// a latch, then subsequent operations on s are undefined.
func (s *S) SetInit(latch, init z.Lit) {
	if init != s.F && init != s.T && init != z.LitNull {
		panic("invalid initial value")
	}
	v := latch.Var()
	s.nodes[v].a = init
}

// SNodeType is the type of a node in an S.
type SNodeType int

// and the types are constants
const (
	SInput SNodeType = iota
	SAnd
	SLatch
	SConst
)

func (t SNodeType) String() string {
	switch t {
	case SInput:
		return "input"
	case SAnd:
		return "and"
	case SLatch:
		return "latch"
	case SConst:
		return "const"
	default:
		panic("wilma!")
	}
}

// Type returns the node type of m.
func (s *S) Type(m z.Lit) SNodeType {
	v := m.Var()
	if v == s.T.Var() {
		return SConst
	}
	n := &s.nodes[v]
	if n.a == z.LitNull && n.b == z.LitNull {
		return SInput
	}
	if n.a.Var() == s.T.Var() || n.a == z.LitNull {
		return SLatch
	}
	return SAnd
}
