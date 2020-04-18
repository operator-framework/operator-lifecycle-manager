// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package logic

import "github.com/irifrance/gini/z"

// Roll creates an unroller of sequential logic into
// combinational logic.
type Roll struct {
	S    *S // the sequential circuit
	C    *C // the resulting comb circuit
	dmap [][]z.Lit
}

// NewRoll creates a new unroller for s
func NewRoll(s *S) *Roll {
	u := &Roll{
		S:    s,
		C:    NewCCap(s.Len() * 10),
		dmap: make([][]z.Lit, s.Len())}
	return u
}

// Len returns the length of the unrolling for literal m.
func (u *Roll) Len(m z.Lit) int {
	v := m.Var()
	return len(u.dmap[v])
}

// MaxLen returns the maximum length of any literal in
// the unrolling.
func (u *Roll) MaxLen() int {
	ns := u.S.nodes
	max := 0
	for i := 1; i < len(ns); i++ {
		v := z.Var(i)
		n := len(u.dmap[v])
		if n > max {
			max = n
		}
	}
	return max
}

// At returns the value of literal m from sequential circuit
// u.S at time/depth d as a literal in u.C
//
// If d < 0, then At panics.
func (u *Roll) At(m z.Lit, d int) z.Lit {
	v := m.Var()
	if len(u.dmap[v]) < d {
		u.At(m, d-1)
	}
	var res, a, b z.Lit
	var n node
	if len(u.dmap[v]) > d {
		res = u.dmap[v][d]
		goto Done
	}
	if v == 1 {
		res = u.C.T
		u.dmap[v] = append(u.dmap[v], res)
		goto Done
	}
	n = u.S.nodes[v]
	if n.b == z.LitNull {
		// input
		res = u.C.Lit()
		u.dmap[v] = append(u.dmap[v], res)
		goto Done
	}
	if d == 0 {
		if n.a == z.LitNull {
			// latch init X
			res = u.C.Lit()
			u.dmap[v] = append(u.dmap[v], res)
			goto Done
		}
		if n.a == u.S.F {
			u.dmap[v] = append(u.dmap[v], u.C.F)
			res = u.C.F
			goto Done
		}
		if n.a == u.S.T {
			u.dmap[v] = append(u.dmap[v], u.C.T)
			res = u.C.T
			goto Done
		}
	}
	if n.a == u.S.F || n.a == u.S.T || n.a == z.LitNull {
		res = u.At(n.b, d-1) // next state time d - 1
		u.dmap[v] = append(u.dmap[v], res)
		goto Done
	}
	a, b = u.At(n.a, d), u.At(n.b, d)
	res = u.C.And(a, b)
	u.dmap[v] = append(u.dmap[v], res)
Done:
	if !m.IsPos() {
		return res.Not()
	}
	return res
}
