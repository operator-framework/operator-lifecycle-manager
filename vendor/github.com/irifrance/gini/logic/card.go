// Copyright 2018 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package logic

import (
	"github.com/irifrance/gini/z"
)

// Card provides an interface for different implementations
// of cardinality constraints.
type Card interface {
	Leq() z.Lit
	Geq() z.Lit
	Less() z.Lit
	Gr() z.Lit
	N() int
}

// CardSort provides cardinality constraints via sorting networks.
//
// Sorting Networks
//
// CardSort uses sorting networks which implement O log(|ms|)**2 compare/swap
// to sort |ms| literals. Each compare/swap is coded symbolically and generates
// 6 clauses with 2 new variables.  The resulting network helps the solver
// achieve arc consistency w.r.t. the variables in ms and the output
// cardinality constraints.  Namely, any partial valuation of ms will cause the
// solver to deduce the corresponding valid and unsat card constraints by unit
// propagation.
//
// While not a best fit coding mechanism for all cases, sorting networks are a
// good choice for a general use single mechanism for coding cardinality
// constraints and hence solving Boolean optimisation problems.
//
// The idea was originally presented by Nicolas Sorensson and Nicolas Een in
// "Translating Pseudo-Boolean Constraints into SAT" Journal on Satisfiability,
// Boolean Modelng, and Computation.
type CardSort struct {
	n   int
	c   *C
	ms  []z.Lit
	tmp []z.Lit
}

// NewCardSort creates a new Card object which gives access to unary Cardinality
// constraints over ms.  The resulting predicates reflect how many of the literals
// in ms are true.
//
func NewCardSort(ms []z.Lit, c *C) *CardSort {
	p := uint(0)
	for 1<<p < len(ms) {
		p++
	}
	ns := make([]z.Lit, 1<<p)
	copy(ns, ms)
	cs := &CardSort{ms: ns, c: c, n: len(ms)}
	for i := len(ms); i < len(ns); i++ {
		ns[i] = c.T
	}
	cs.sort(0, len(ns))
	return cs
}

// Less returns a literal which is true iff and only if the number of true
// literals over the set to be counted does not exceed b
func (c *CardSort) Less(b int) z.Lit {
	return c.Leq(b - 1)
}

// Leq implemets Card.Leq.
func (c *CardSort) Leq(b int) z.Lit {
	if b >= c.n {
		return c.c.T
	}
	if b < 0 {
		return c.c.F
	}
	return c.ms[(c.n-1)-b].Not()
}

// Geq implements Card.Geq.
func (c *CardSort) Geq(b int) z.Lit {
	if b <= 0 {
		return c.c.T
	}
	if b >= c.n+1 {
		return c.c.F
	}
	return c.Leq(b - 1).Not()
}

// Gr implements Card.Gr.
func (c *CardSort) Gr(b int) z.Lit {
	return c.Geq(b + 1)
}

// N returns the number of literals whose
// cardinality is tested.  N is len(ms) when
// the caller calls
//
//    NewCard(ms, va)
func (c *CardSort) N() int {
	return c.n
}

func (c *CardSort) sort(l, h int) {
	if h-l <= 1 {
		return
	}
	m := l + (h-l)/2
	c.sort(l, m)
	c.sort(m, h)
	c.merge(l, h, 1)
}

//
// odd even merge sort
//
func (c *CardSort) merge(l, h, s int) {
	if h <= l+s {
		return
	}
	//fmt.Printf("merge [%d..%d) by %d\n", l, h, s)
	var ml, mh z.Lit
	ss := 2 * s
	if ss >= h-l {
		ml, mh = c.cas(l, l+s)
		c.ms[l], c.ms[l+s] = ml, mh
		return
	}
	c.merge(l, h, ss)
	c.merge(l+s, h, ss)
	lim := h - s
	for i := l + s; i < lim; i += ss {
		ml, mh = c.cas(i, i+s)
		c.ms[i], c.ms[i+s] = ml, mh
	}
}

// compare-and-swap (low-high)
func (c *CardSort) cas(i, j int) (z.Lit, z.Lit) {
	mi, mj := c.ms[i], c.ms[j]
	l := c.c.And(mi, mj)
	h := c.c.Or(mi, mj)
	return l, h
}
