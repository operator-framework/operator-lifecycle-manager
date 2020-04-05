// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"math"

	"github.com/irifrance/gini/z"
)

const (
	gBumpInc       = float64(1.0)
	gBumpDecay     = float64(0.95)
	gBumpLim       = float64(1e100)
	gDecayMin      = float64(0.67)
	gDecayMax      = float64(0.935)
	gDecayMaxMax   = float64(0.9875)
	gDecayMaxDecay = float64(0.9999)
)

type Guess struct {
	pos   []int
	vhp   []z.Var
	heat  []float64
	cache []int8

	// decay structure
	decays        int64
	restartDecays int
	decayMax      float64
	decayMin      float64
	decayMaxMax   float64
	decayMaxDecay float64

	bumpInc   float64
	bumpDecay float64
	bumpLim   float64

	rescales int64
	guesses  int64
}

func newGuess(capHint int) *Guess {
	top := capHint + 1
	g := &Guess{
		pos:   make([]int, top),
		vhp:   make([]z.Var, 0, top),
		heat:  make([]float64, top),
		cache: make([]int8, top),

		decays:        0,
		restartDecays: 0,
		decayMin:      gDecayMin,
		decayMax:      gDecayMax,
		decayMaxMax:   gDecayMaxMax,
		decayMaxDecay: gDecayMaxDecay,

		bumpInc:   gBumpInc,
		bumpDecay: gBumpDecay,
		bumpLim:   gBumpLim}
	for i := range g.pos {
		g.pos[i] = -1
	}
	return g
}

func NewGuessCdb(cdb *Cdb) *Guess {
	top := int(cdb.Vars.Top)
	g := newGuess(top)
	g.vhp = g.vhp[0:int(cdb.Vars.Max)]
	for i := 1; i <= int(cdb.Vars.Max); i++ {
		u := z.Var(i)
		j := i - 1
		g.vhp[j] = u
		g.pos[i] = j
	}
	inc := 0.001
	cdb.Forall(func(p z.C, h Chd, ms []z.Lit) {
		for _, m := range ms {
			if h.Size() >= 16 {
				continue
			}
			g.heat[m.Var()] += inc
		}
	})
	g.heapify()
	return g
}

// Guess finds the first unassigned variable and returns
// its cached value.
func (g *Guess) Guess(vals []int8) z.Lit {
	vhp := g.vhp
	n := len(vhp)
	var v z.Var
	for n > 0 {
		n--
		v = g.pop()
		if vals[v.Pos()] == 0 {
			g.guesses++
			switch g.cache[v] {
			case -1:
				return v.Neg()
			case 1:
				return v.Pos()
			default:
				return v.Pos()
			}
		}
	}
	return z.LitNull
}

// Bump increases the heat of the variable associated with m
func (g *Guess) Bump(m z.Lit) bool {
	v := m.Var()
	p := g.pos[v]
	h := g.heat[v] + g.bumpInc
	g.heat[v] = h
	var res = false
	if h > g.bumpLim {
		g.rescales++
		scale := 1.0 / g.bumpLim
		for i, t := range g.heat {
			g.heat[i] = t * scale
		}
		g.bumpInc *= scale
		res = true
	}
	if p != -1 {
		g.down(p, len(g.vhp))
		g.up(p)
	}
	return res
}

func (g *Guess) readStats(st *Stats) {
	st.Guesses += g.guesses
	st.GuessRescales += g.rescales
	g.guesses = 0
	g.rescales = 0
}

// Decay increases the bump quantity geometrically.
func (g *Guess) Decay() {
	g.decays++
	rat := float64(g.decays) / float64(g.restartDecays)
	v := math.Exp(-100.0 * (1.0 - rat))
	decayRange := g.decayMax - g.decayMin
	g.bumpDecay = g.decayMax - v*decayRange
	g.bumpInc /= g.bumpDecay
	//log.Printf("guess %d/%d %.5f m %.5f\n", g.decays, g.restartDecays, g.bumpDecay, g.decayMax)
}

// Guess
func (g *Guess) nextRestart(nxt int) {
	g.decays = 0
	g.restartDecays = nxt
	g.decayMax = g.decayMaxDecay*g.decayMax + (1.0-g.decayMaxDecay)*g.decayMaxMax
}

func (g *Guess) heapify() {
	n := len(g.vhp)
	for i := n/2 - 1; i >= 0; i-- {
		g.down(i, n)
	}
}

// Len returns how many candidate variables are in
// the heap.  Note the heap may contain assigned
// variables.
func (g *Guess) Len() int {
	return len(g.vhp)
}

// At returns the i'th element of the underlying heap.
func (g *Guess) At(i int) z.Var {
	return g.vhp[i]
}

func (g *Guess) pop() z.Var {
	n := len(g.vhp) - 1
	g.swap(0, n)
	g.down(0, n)
	v := g.vhp[n]
	g.vhp = g.vhp[:n]
	g.pos[v] = -1
	return v
}

func (g *Guess) Push(m z.Lit) {
	v := m.Var()
	g.cache[v] = m.Sign()
	if g.pos[v] != -1 {
		return
	}
	g.pos[v] = len(g.vhp)
	g.vhp = append(g.vhp, v)
	g.up(len(g.vhp) - 1)
}

func (g *Guess) Heat() float64 {
	return g.heat[0]
}

// Copy makes a copy of g.
func (g *Guess) Copy() *Guess {
	other := &Guess{
		pos:       make([]int, len(g.pos), cap(g.pos)),
		vhp:       make([]z.Var, len(g.vhp), cap(g.vhp)),
		heat:      make([]float64, len(g.heat), cap(g.heat)),
		cache:     make([]int8, len(g.cache), cap(g.cache)),
		bumpInc:   g.bumpInc,
		bumpDecay: g.bumpDecay,
		bumpLim:   g.bumpLim}
	copy(other.pos, g.pos)
	copy(other.vhp, g.vhp)
	copy(other.heat, g.heat)
	copy(other.cache, g.cache)
	return other
}

func (g *Guess) has(vals []int8) bool {
	for _, v := range g.vhp {
		if vals[v.Pos()] == 0 {
			return true
		}
	}
	return false
}

// just here for reference, really
func (g *Guess) fix(m z.Lit) {
	v := m.Var()
	i := g.pos[v]
	g.down(i, len(g.vhp))
	g.up(i)
}

func (g *Guess) up(j int) {
	t := g.heat
	vhp := g.vhp
	for {
		i := (j - 1) / 2
		if i == j || t[vhp[j]] <= t[vhp[i]] {
			break
		}
		g.swap(i, j)
		j = i
	}
}

func (g *Guess) down(i, n int) {
	t := g.heat
	vhp := g.vhp
	var j, j1, j2 int
	for {
		j1 = 2*i + 1
		if j1 >= n || j1 < 0 { // j1 < 0 after int overflow
			break
		}
		j = j1 // left child
		j2 = j1 + 1
		if j2 < n && t[vhp[j1]] <= t[vhp[j2]] {
			j = j2 // = 2*i + 2  // right child
		}
		if t[vhp[j]] <= t[vhp[i]] {
			break
		}
		g.swap(i, j)
		i = j
	}
}

func (g *Guess) swap(i, j int) {
	vhp := g.vhp
	u, v := vhp[i], vhp[j]
	vhp[i], vhp[j] = v, u
	pos := g.pos
	pos[u], pos[v] = j, i
}

func (g *Guess) growToVar(u z.Var) {
	w := u + 1
	d := make([]z.Var, len(g.vhp), w)
	copy(d, g.vhp)
	g.vhp = d

	p := make([]int, w)
	copy(p, g.pos)
	for i := len(g.pos); i < int(w); i++ {
		p[i] = len(g.vhp)
		g.vhp = append(g.vhp, z.Var(i))
	}
	g.pos = p

	h := make([]float64, w)
	copy(h, g.heat)
	g.heat = h

	c := make([]int8, w)
	copy(c, g.cache)
	g.cache = c
	g.heapify()
}
