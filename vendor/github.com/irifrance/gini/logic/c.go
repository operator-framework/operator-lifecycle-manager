// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package logic

import (
	"github.com/irifrance/gini/inter"
	"github.com/irifrance/gini/z"
)

// C represents a formula or combinational circuit.
type C struct {
	nodes  []node   // list of all nodes
	strash []uint32 // strash
	F      z.Lit    // false literal
	T      z.Lit
}

type node struct {
	a z.Lit  // input a
	b z.Lit  // input b
	n uint32 // next strash
}

// NewC create a new circuit.
func NewC() *C {
	phi := &C{}
	initC(phi, 128)
	return phi
}

// NewCCap creates a new combinational circuit with initial capacity capHint.
func NewCCap(capHint int) *C {
	phi := &C{}
	initC(phi, capHint)
	return phi
}

func initC(c *C, capHint int) {
	if capHint < 2 {
		capHint = 2
	}
	c.nodes = make([]node, 2, capHint)
	c.strash = make([]uint32, capHint)
	c.F = z.Var(1).Neg()
	c.T = c.F.Not()
}

// ToCnf creates a conjunctive normal form of p in
// adder.
//
// Adder uses basic Tseitinization.
func (c *C) ToCnf(dst inter.Adder) {
	dst.Add(c.T)
	dst.Add(0)
	e := len(c.nodes)
	for i := 1; i < e; i++ {
		n := c.nodes[i]
		a := n.a
		if a == z.LitNull || a == c.F || a == c.T {
			continue
		}
		b := n.b
		g := z.Var(i).Pos()
		addAnd(dst, g, a, b)
	}
}

// Copy makes a copy of `c`.
func (c *C) Copy() *C {
	ns := make([]node, len(c.nodes))
	st := make([]uint32, len(c.strash))
	copy(ns, c.nodes)
	copy(st, c.strash)
	return &C{
		nodes:  ns,
		strash: st,
		T:      c.T,
		F:      c.F}
}

func addAnd(dst inter.Adder, g, a, b z.Lit) {
	dst.Add(g.Not())
	dst.Add(a)
	dst.Add(0)
	dst.Add(g.Not())
	dst.Add(b)
	dst.Add(0)
	dst.Add(g)
	dst.Add(a.Not())
	dst.Add(b.Not())
	dst.Add(0)
}

// ToCnfFrom creates a conjunctive normal form of p in
// adder, including only the part of the circuit reachable
// from some root in roots.
func (c *C) ToCnfFrom(dst inter.Adder, roots ...z.Lit) {
	c.CnfSince(dst, nil, roots...)
}

// CnfSince adds the circuit rooted at roots to dst assuming mark marks all
// already added nodes in the circuit`.  CnfSince returns marks from previously
// marked nodes and the total number of nodes added.  If mark is nil or does
// not have sufficient capacity, then new storage is created with a copy of
// mark.
func (c *C) CnfSince(dst inter.Adder, mark []int8, roots ...z.Lit) ([]int8, int) {
	if cap(mark) < len(c.nodes) {
		tmp := make([]int8, (len(c.nodes)*5)/3)
		copy(tmp, mark)
		mark = tmp
	} else if len(mark) < len(c.nodes) {
		start := len(mark)
		mark = mark[:len(c.nodes)]
		for i := start; i < len(c.nodes); i++ {
			mark[i] = 0
		}
	}
	mark = mark[:len(c.nodes)]
	ttl := 0
	if mark[1] != 1 {
		dst.Add(c.T)
		dst.Add(0)
		mark[1] = 1
		ttl++
	}
	var vis func(m z.Lit)
	vis = func(m z.Lit) {
		v := m.Var()
		if mark[v] == 1 {
			return
		}
		n := &c.nodes[v]
		if n.a == z.LitNull || n.a == c.T || n.a == c.F {
			mark[v] = 1
			return
		}
		vis(n.a)
		vis(n.b)
		g := m
		if !m.IsPos() {
			g = m.Not()
		}
		addAnd(dst, g, n.a, n.b)
		mark[v] = 1
		ttl++
	}
	for _, root := range roots {
		vis(root)
	}
	return mark, ttl
}

// Len returns the length of C, the number of internal nodes used to represent
// C.
func (c *C) Len() int {
	return len(c.nodes)
}

// At returns the i'th element.  Elements from 0..Len(c) are in topological
// order:  if i < j then c.At(j) is not reachable from c.At(i) via the edge
// relation defined by c.Ins().  All elements are positive literals.
//
// One variable for internal use, with index 1, is created when c is created.
// All other variables created by NewIn, And, ...  are created in sequence
// starting with index 2.  Internal variables may be created by c.  c.Len() - 1
// is the maximal index of a variable.
//
// Hence, the implementation of At(i) is simply z.Var(i).Pos().
func (c *C) At(i int) z.Lit {
	return z.Var(i).Pos()
}

// Lit returns a new variable/input to p.
func (c *C) Lit() z.Lit {
	m := len(c.nodes)
	c.newNode()
	return z.Var(m).Pos()
}

// InPos returns the positions of all inputs
// in c in the sequence attainable via Len() and
// At().  The result is placed in dst if there is space.
//
// If c is part of S, then latches are not included.
func (c *C) InPos(dst []int) []int {
	dst = dst[:0]
	for i, n := range c.nodes {
		if i == 0 {
			continue
		}
		if n.a == z.LitNull && n.b == z.LitNull {
			dst = append(dst, i)
		}
	}
	return dst
}

// Eval evaluates the circuit with values vs, where
// for each literal m in the circuit, vs[i] contains
// the value for m's variable if m.Var() == i.
//
// vs should contain values for all inputs.  In case
// `c` is embedded in a sequential circuit `s`, then
// the inputs include the latches of `s`.
func (c *C) Eval(vs []bool) {
	N := len(c.nodes)
	vs[1] = true
	for i := 2; i < N; i++ {
		n := &c.nodes[i]
		if n.a < 4 {
			continue
		}
		a, b := n.a, n.b
		va, vb := vs[a.Var()], vs[b.Var()]
		if !a.IsPos() {
			va = !va
		}
		if !b.IsPos() {
			vb = !vb
		}
		g := z.Var(i)
		vs[g] = va && vb
	}
}

// Eval64 is like Eval but evaluates 64 different inputs in
// parallel as the bits of a uint64.
func (c *C) Eval64(vs []uint64) {
	N := len(c.nodes)
	vs[1] = (1 << 63) - 1
	for i := 2; i < N; i++ {
		n := &c.nodes[i]
		if n.a < 4 {
			continue
		}
		a, b := n.a, n.b
		va, vb := vs[a.Var()], vs[b.Var()]
		if !a.IsPos() {
			va = ^va
		}
		if !b.IsPos() {
			vb = ^vb
		}
		g := z.Var(i)
		vs[g] = va & vb
	}
}

// And returns a literal equivalent to "a and b", which may
// be a new variable.
func (p *C) And(a, b z.Lit) z.Lit {
	if a == b {
		return a
	}
	if a == b.Not() {
		return p.F
	}
	if a > b {
		a, b = b, a
	}
	if a == p.F {
		return p.F
	}
	if a == p.T {
		return b
	}
	c := strashCode(a, b)
	l := uint32(cap(p.nodes))
	i := c % l
	si := p.strash[i]
	for {
		n := &p.nodes[si]
		if n.a == a && n.b == b {
			return z.Var(si).Pos()
		}
		if n.n == 0 {
			break
		}
		si = n.n
	}
	m, j := p.newNode()
	m.a = a
	m.b = b
	k := c % uint32(cap(p.nodes))
	m.n = p.strash[k]
	p.strash[k] = j
	return z.Var(j).Pos()
}

// Ands constructs a conjunction of a sequence of literals.
// If ms is empty, then Ands returns p.T.
func (c *C) Ands(ms ...z.Lit) z.Lit {
	a := c.T
	for _, m := range ms {
		a = c.And(a, m)
	}
	return a
}

// Or constructs a literal which is the disjunction of a and b.
func (c *C) Or(a, b z.Lit) z.Lit {
	nor := c.And(a.Not(), b.Not())
	return nor.Not()
}

// Ors constructs a literal which is the disjuntion of the literals in ms.
// If ms is empty, then Ors returns p.F
func (c *C) Ors(ms ...z.Lit) z.Lit {
	d := c.F
	for _, m := range ms {
		d = c.Or(d, m)
	}
	return d
}

// Implies constructs a literal which is equivalent to (a implies b).
func (c *C) Implies(a, b z.Lit) z.Lit {
	return c.Or(a.Not(), b)
}

// Xor constructs a literal which is equivalent to (a xor b).
func (c *C) Xor(a, b z.Lit) z.Lit {
	return c.Or(c.And(a, b.Not()), c.And(a.Not(), b))
}

// Choice constructs a literal which is equivalent to
//  if i then t else e
func (c *C) Choice(i, t, e z.Lit) z.Lit {
	return c.Or(c.And(i, t), c.And(i.Not(), e))
}

// Ins returns the children/ operands of m.
//
//  If m is an input, then, Ins returns z.LitNull, z.LitNull
//  If m is an and, then Ins returns the two conjuncts
func (c *C) Ins(m z.Lit) (z.Lit, z.Lit) {
	v := m.Var()
	n := c.nodes[v]
	return n.a, n.b
}

// CardSort creates a CardSort object whose
// cardinality predicates over ms are encoded in c.
func (c *C) CardSort(ms []z.Lit) *CardSort {
	return NewCardSort(ms, c)
}

func (c *C) newNode() (*node, uint32) {
	if len(c.nodes) == cap(c.nodes) {
		c.grow()
	}
	id := len(c.nodes)
	c.nodes = c.nodes[:id+1]
	return &c.nodes[id], uint32(id)
}

func (p *C) grow() {
	newCap := cap(p.nodes) * 2
	nodes := make([]node, cap(p.nodes), newCap)
	strash := make([]uint32, newCap)
	copy(nodes, p.nodes)
	ucap := uint32(newCap)
	for i := range nodes {
		n := &nodes[i]
		if n.a == 0 || n.a == p.F || n.a == p.T {
			continue
		}
		c := strashCode(n.a, n.b)
		j := c % ucap
		n.n = strash[j]
		strash[j] = uint32(i)
	}
	p.nodes = nodes
	p.strash = strash
}

func strashCode(a, b z.Lit) uint32 {
	return uint32((a << 13) * b)
}
