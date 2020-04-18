// Copyright 2016 The Gini Authors. All rights reserved.  Use of this source
// code is governed by a license that can be found in the License file.

package xo

import (
	"fmt"
	"strings"

	"github.com/irifrance/gini/z"
)

type Vars struct {
	Max     z.Var // maximum value of a used variable
	Top     z.Var // number of allocated variables, 1-indexed
	Vals    []int8
	Reasons []z.C
	Levels  []int
	Watches [][]Watch
}

func NewVars(capHint int) *Vars {
	if capHint < 3 {
		capHint = 3
	}
	top := capHint + 1
	v := &Vars{Max: z.Var(0),
		Top:     z.Var(top),
		Reasons: make([]z.C, top),
		Levels:  make([]int, top),
		Vals:    make([]int8, 2*top),
		Watches: make([][]Watch, 2*top)}
	for i := range v.Watches {
		v.Watches[i] = make([]Watch, 0, 8)
	}
	return v
}

func (v *Vars) Set(m z.Lit) {
	v.Vals[m] = 1
	v.Vals[m.Not()] = -1
}

func (vars *Vars) String() string {
	parts := make([]string, 0, vars.Max)
	for v := z.Var(1); v < vars.Max; v++ {
		parts = append(parts,
			fmt.Sprintf("%d %d %s l%d", v, vars.Vals[v.Pos()], vars.Reasons[v],
				vars.Levels[v]))
	}
	return strings.Join(parts, "\n")
}

func (v *Vars) Sign(m z.Lit) int8 {
	return v.Vals[m]
}

func (v *Vars) readStats(st *Stats) {
	st.Vars = int(v.Max)
}

func (v *Vars) growToVar(u z.Var) {

	w := u + 1
	vs := make([]int8, 2*w)
	copy(vs, v.Vals)
	v.Vals = vs

	rs := make([]z.C, w)
	copy(rs, v.Reasons)
	v.Reasons = rs

	ls := make([]int, 2*w)
	copy(ls, v.Levels)
	v.Levels = ls

	ws := make([][]Watch, w*2)
	copy(ws, v.Watches)
	v.Watches = ws
	v.Top = u
}

func (v *Vars) Copy() *Vars {
	other := &Vars{
		Max:     v.Max,
		Top:     v.Top,
		Vals:    make([]int8, len(v.Vals), cap(v.Vals)),
		Reasons: make([]z.C, len(v.Reasons), cap(v.Reasons)),
		Levels:  make([]int, len(v.Levels), cap(v.Levels)),
		Watches: make([][]Watch, len(v.Watches), cap(v.Watches))}
	copy(other.Vals, v.Vals)
	copy(other.Reasons, v.Reasons)
	copy(other.Levels, v.Levels)
	for i, ws := range v.Watches {
		other.Watches[i] = make([]Watch, len(ws))
		copy(other.Watches[i], ws)
	}
	return other
}
